package eventsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/reference"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	virtwait "kubevirt.io/kubevirt/pkg/apimachinery/wait"
	diskutils "kubevirt.io/kubevirt/pkg/ephemeral-disk-utils"
	com "kubevirt.io/kubevirt/pkg/handler-launcher-com"
	"kubevirt.io/kubevirt/pkg/handler-launcher-com/notify/info"
	notifyv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/notify/v1"
	grpcutil "kubevirt.io/kubevirt/pkg/util/net/grpc"
	"kubevirt.io/kubevirt/pkg/virt-launcher-common/api"
)

var (
	// add older version when supported
	// don't use the variable in pkg/handler-launcher-com/notify/v1/version.go in order to detect version mismatches early
	supportedNotifyVersions = []uint32{1}
)

type NotifyClient struct {
	v1client         notifyv1.NotifyClient
	conn             *grpc.ClientConn
	connLock         sync.Mutex
	pipeSocketPath   string
	legacySocketPath string

	intervalTimeout time.Duration
	sendTimeout     time.Duration
	totalTimeout    time.Duration
}

var (
	defaultIntervalTimeout = 1 * time.Second
	defaultSendTimeout     = 5 * time.Second
	defaultTotalTimeout    = 20 * time.Second
)

func NewNotifyClient(virtShareDir string) *NotifyClient {
	return &NotifyClient{
		pipeSocketPath:   filepath.Join(virtShareDir, "domain-notify-pipe.sock"),
		legacySocketPath: filepath.Join(virtShareDir, "domain-notify.sock"),
		intervalTimeout:  defaultIntervalTimeout,
		sendTimeout:      defaultSendTimeout,
		totalTimeout:     defaultTotalTimeout,
	}
}

var (
	schemeBuilder = runtime.NewSchemeBuilder(v1.AddKnownTypesGenerator(v1.GroupVersions))
	addToScheme   = schemeBuilder.AddToScheme
	scheme        = runtime.NewScheme()
)

func init() {
	addToScheme(scheme)
}

func negotiateVersion(infoClient info.NotifyInfoClient) (uint32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := infoClient.Info(ctx, &info.NotifyInfoRequest{})
	if err != nil {
		return 0, fmt.Errorf("could not check cmd server version: %v", err)
	}
	version, err := com.GetHighestCompatibleVersion(info.SupportedNotifyVersions, supportedNotifyVersions)
	if err != nil {
		return 0, err
	}

	switch version {
	case 1:
		// fall-through for all supported versions
	default:
		return 0, fmt.Errorf("cmd v1client version %v not implemented yet", version)
	}

	return version, nil
}

// used by unit tests
func (n *NotifyClient) SetCustomTimeouts(interval, send, total time.Duration) {
	n.intervalTimeout = interval
	n.sendTimeout = send
	n.totalTimeout = total

}

func (n *NotifyClient) detectSocketPath() string {

	// use the legacy domain socket if it exists. This would
	// occur if the vmi was started with a hostPath shared mount
	// using our old method for virt-handler to virt-launcher communication
	exists, _ := diskutils.FileExists(n.legacySocketPath)
	if exists {
		return n.legacySocketPath
	}

	// default to using the new pipe socket
	return n.pipeSocketPath
}

func (n *NotifyClient) connect() error {
	if n.conn != nil {
		// already connected
		return nil
	}

	socketPath := n.detectSocketPath()

	// dial socket
	conn, err := grpcutil.DialSocketWithTimeout(socketPath, 5)
	if err != nil {
		log.Log.Reason(err).Infof("failed to dial notify socket: %s", socketPath)
		return err
	}

	version, err := negotiateVersion(info.NewNotifyInfoClient(conn))
	if err != nil {
		log.Log.Reason(err).Infof("failed to negotiate version")
		conn.Close()
		return err
	}

	// create cmd v1client
	switch version {
	case 1:
		client := notifyv1.NewNotifyClient(conn)
		n.v1client = client
		n.conn = conn
	default:
		conn.Close()
		return fmt.Errorf("cmd v1client version %v not implemented yet", version)
	}

	log.Log.Infof("Successfully connected to domain notify socket at %s", socketPath)
	return nil
}

// TODO PLUGINDEV: Except the StartDomainNotifier function's bits where interaction w Libvirt etc is present, rest of this file will be part of the common virt-launcher code.
// TODO PLUGINDEV: SendDomainEvent and SendK8sEvent should be exposed to the diff virt-launchers

func (n *NotifyClient) SendDomainEvent(event watch.Event) error {

	var domainJSON []byte
	var statusJSON []byte
	var err error

	if event.Type == watch.Error {
		status := event.Object.(*metav1.Status)
		statusJSON, err = json.Marshal(status)
		if err != nil {
			log.Log.Reason(err).Infof("JSON marshal of notify ERROR event failed")
			return err
		}
	} else {
		domain := event.Object.(*api.Domain)
		domainJSON, err = json.Marshal(domain)
		if err != nil {
			log.Log.Reason(err).Infof("JSON marshal of notify event failed")
			return err
		}
	}
	request := notifyv1.DomainEventRequest{
		DomainJSON: domainJSON,
		StatusJSON: statusJSON,
		EventType:  string(event.Type),
	}

	var response *notifyv1.Response
	err = virtwait.PollImmediately(n.intervalTimeout, n.totalTimeout, func(ctx context.Context) (done bool, err error) {
		n.connLock.Lock()
		defer n.connLock.Unlock()

		err = n.connect()
		if err != nil {
			log.Log.Reason(err).Errorf("Failed to connect to notify server")
			return false, nil
		}

		ctx, cancel := context.WithTimeout(ctx, n.sendTimeout)
		defer cancel()
		response, err = n.v1client.HandleDomainEvent(ctx, &request)
		if err != nil {
			log.Log.Reason(err).Errorf("Failed to send domain notify event. closing connection.")
			n._close()
			return false, nil
		}

		return true, nil

	})

	if err != nil {
		log.Log.Reason(err).Infof("Failed to send domain notify event")
		return err
	} else if response.Success != true {
		msg := fmt.Sprintf("failed to notify domain event: %s", response.Message)
		return fmt.Errorf(msg)
	}

	return nil
}

func (n *NotifyClient) SendK8sEvent(vmi *v1.VirtualMachineInstance, severity string, reason string, message string) error {
	vmiRef, err := reference.GetReference(scheme, vmi)
	if err != nil {
		return err
	}

	event := k8sv1.Event{
		InvolvedObject: *vmiRef,
		Type:           severity,
		Reason:         reason,
		Message:        message,
	}

	json, err := json.Marshal(event)
	if err != nil {
		return err
	}

	request := notifyv1.K8SEventRequest{
		EventJSON: json,
	}

	var response *notifyv1.Response
	err = virtwait.PollImmediately(n.intervalTimeout, n.totalTimeout, func(ctx context.Context) (done bool, err error) {
		n.connLock.Lock()
		defer n.connLock.Unlock()

		err = n.connect()
		if err != nil {
			log.Log.Reason(err).Errorf("Failed to connect to notify server")
			return false, nil
		}

		ctx, cancel := context.WithTimeout(ctx, n.sendTimeout)
		defer cancel()
		response, err = n.v1client.HandleK8SEvent(ctx, &request)
		if err != nil {
			log.Log.Reason(err).Errorf("Failed to send k8s notify event. closing connection.")
			n._close()
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return err
	} else if response.Success != true {
		msg := fmt.Sprintf("failed to notify k8s event: %s", response.Message)
		return fmt.Errorf(msg)
	}

	return nil
}

func (n *NotifyClient) _close() {
	if n.conn != nil {
		n.conn.Close()
		n.conn = nil
	}
}

func (n *NotifyClient) Close() {
	n.connLock.Lock()
	defer n.connLock.Unlock()
	n._close()

}
