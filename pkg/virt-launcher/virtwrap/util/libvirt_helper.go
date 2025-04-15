package util

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"strings"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"libvirt.org/go/libvirt"

	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/hooks"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/cli"
)

const (
	vmmConfPathPattern              = "/etc/libvirt/%s.conf"
	vmmModularDaemonConfPathPattern = "/etc/libvirt/%s.conf"
	libvirtRuntimePath              = "/var/run/libvirt"
	libvirtHomePath                 = "/var/run/kubevirt-private/libvirt"
	vmmNonRootConfPathPattern       = libvirtHomePath + "/%s.conf"
)

var LifeCycleTranslationMap = map[libvirt.DomainState]api.LifeCycle{
	libvirt.DOMAIN_NOSTATE:     api.NoState,
	libvirt.DOMAIN_RUNNING:     api.Running,
	libvirt.DOMAIN_BLOCKED:     api.Blocked,
	libvirt.DOMAIN_PAUSED:      api.Paused,
	libvirt.DOMAIN_SHUTDOWN:    api.Shutdown,
	libvirt.DOMAIN_SHUTOFF:     api.Shutoff,
	libvirt.DOMAIN_CRASHED:     api.Crashed,
	libvirt.DOMAIN_PMSUSPENDED: api.PMSuspended,
}

var ShutdownReasonTranslationMap = map[libvirt.DomainShutdownReason]api.StateChangeReason{
	libvirt.DOMAIN_SHUTDOWN_UNKNOWN: api.ReasonUnknown,
	libvirt.DOMAIN_SHUTDOWN_USER:    api.ReasonUser,
}

var ShutoffReasonTranslationMap = map[libvirt.DomainShutoffReason]api.StateChangeReason{
	libvirt.DOMAIN_SHUTOFF_UNKNOWN:       api.ReasonUnknown,
	libvirt.DOMAIN_SHUTOFF_SHUTDOWN:      api.ReasonShutdown,
	libvirt.DOMAIN_SHUTOFF_DESTROYED:     api.ReasonDestroyed,
	libvirt.DOMAIN_SHUTOFF_CRASHED:       api.ReasonCrashed,
	libvirt.DOMAIN_SHUTOFF_MIGRATED:      api.ReasonMigrated,
	libvirt.DOMAIN_SHUTOFF_SAVED:         api.ReasonSaved,
	libvirt.DOMAIN_SHUTOFF_FAILED:        api.ReasonFailed,
	libvirt.DOMAIN_SHUTOFF_FROM_SNAPSHOT: api.ReasonFromSnapshot,
}

var CrashedReasonTranslationMap = map[libvirt.DomainCrashedReason]api.StateChangeReason{
	libvirt.DOMAIN_CRASHED_UNKNOWN:  api.ReasonUnknown,
	libvirt.DOMAIN_CRASHED_PANICKED: api.ReasonPanicked,
}

var PausedReasonTranslationMap = map[libvirt.DomainPausedReason]api.StateChangeReason{
	libvirt.DOMAIN_PAUSED_UNKNOWN:         api.ReasonPausedUnknown,
	libvirt.DOMAIN_PAUSED_USER:            api.ReasonPausedUser,
	libvirt.DOMAIN_PAUSED_MIGRATION:       api.ReasonPausedMigration,
	libvirt.DOMAIN_PAUSED_SAVE:            api.ReasonPausedSave,
	libvirt.DOMAIN_PAUSED_DUMP:            api.ReasonPausedDump,
	libvirt.DOMAIN_PAUSED_IOERROR:         api.ReasonPausedIOError,
	libvirt.DOMAIN_PAUSED_WATCHDOG:        api.ReasonPausedWatchdog,
	libvirt.DOMAIN_PAUSED_FROM_SNAPSHOT:   api.ReasonPausedFromSnapshot,
	libvirt.DOMAIN_PAUSED_SHUTTING_DOWN:   api.ReasonPausedShuttingDown,
	libvirt.DOMAIN_PAUSED_SNAPSHOT:        api.ReasonPausedSnapshot,
	libvirt.DOMAIN_PAUSED_CRASHED:         api.ReasonPausedCrashed,
	libvirt.DOMAIN_PAUSED_STARTING_UP:     api.ReasonPausedStartingUp,
	libvirt.DOMAIN_PAUSED_POSTCOPY:        api.ReasonPausedPostcopy,
	libvirt.DOMAIN_PAUSED_POSTCOPY_FAILED: api.ReasonPausedPostcopyFailed,
}

var getHookManager = hooks.GetManager

func ConvState(status libvirt.DomainState) api.LifeCycle {
	return LifeCycleTranslationMap[status]
}

func ConvReason(status libvirt.DomainState, reason int) api.StateChangeReason {
	switch status {
	case libvirt.DOMAIN_SHUTDOWN:
		return ShutdownReasonTranslationMap[libvirt.DomainShutdownReason(reason)]
	case libvirt.DOMAIN_SHUTOFF:
		return ShutoffReasonTranslationMap[libvirt.DomainShutoffReason(reason)]
	case libvirt.DOMAIN_CRASHED:
		return CrashedReasonTranslationMap[libvirt.DomainCrashedReason(reason)]
	case libvirt.DOMAIN_PAUSED:
		return PausedReasonTranslationMap[libvirt.DomainPausedReason(reason)]
	default:
		return api.ReasonUnknown
	}
}

// base64.StdEncoding.EncodeToString
func SetDomainSpecStr(virConn cli.Connection, vmi *v1.VirtualMachineInstance, wantedSpec string) (cli.VirDomain, error) {
	log.Log.Object(vmi).V(2).Infof("Domain XML generated. Base64 dump %s", base64.StdEncoding.EncodeToString([]byte(wantedSpec)))
	dom, err := virConn.DomainDefineXML(wantedSpec)
	if err != nil {
		log.Log.Object(vmi).Reason(err).Error("Defining the VirtualMachineInstance failed.")
		return nil, err
	}
	return dom, nil
}

func SetDomainSpecStrWithHooks(virConn cli.Connection, vmi *v1.VirtualMachineInstance, wantedSpec *api.DomainSpec) (cli.VirDomain, error) {
	hooksManager := getHookManager()
	domainSpec, err := hooksManager.OnDefineDomain(wantedSpec, vmi)
	if err != nil {
		return nil, err
	}

	// update wantedSpec to reflect changes made to domain spec by hooks
	domainSpecObj := &api.DomainSpec{}
	if err = xml.Unmarshal([]byte(domainSpec), domainSpecObj); err != nil {
		return nil, err
	}
	domainSpecObj.DeepCopyInto(wantedSpec)

	return SetDomainSpecStr(virConn, vmi, domainSpec)
}

// GetDomainSpecWithRuntimeInfo return the active domain XML with runtime information embedded
func GetDomainSpecWithRuntimeInfo(dom cli.VirDomain) (*api.DomainSpec, error) {

	// get libvirt xml with runtime status
	activeSpec, err := GetDomainSpecWithFlags(dom, 0)
	if err != nil {
		log.Log.Reason(err).Error("failed to get domain spec")
		return nil, err
	}

	return activeSpec, nil
}

// GetDomainSpec return the domain XML without runtime information.
// The result XML is merged from inactive XML and migratable XML.
func GetDomainSpec(status libvirt.DomainState, dom cli.VirDomain) (*api.DomainSpec, error) {

	var spec, inactiveSpec *api.DomainSpec
	var err error

	inactiveSpec, err = GetDomainSpecWithFlags(dom, libvirt.DOMAIN_XML_INACTIVE)

	if err != nil {
		return nil, err
	}

	spec = inactiveSpec
	// libvirt (the whole server) sometimes block indefinitely if a guest-shutdown was performed
	// and we immediately ask it after the successful shutdown for a migratable xml.
	if !cli.IsDown(status) {
		spec, err = GetDomainSpecWithFlags(dom, libvirt.DOMAIN_XML_MIGRATABLE)
		if err != nil {
			return nil, err
		}
	}

	return spec, nil
}

func GetDomainSpecWithFlags(dom cli.VirDomain, flags libvirt.DomainXMLFlags) (*api.DomainSpec, error) {
	domain := &api.DomainSpec{}
	domxml, err := dom.GetXMLDesc(flags)
	if err != nil {
		return nil, err
	}
	err = xml.Unmarshal([]byte(domxml), domain)
	if err != nil {
		return nil, err
	}

	return domain, nil
}

// returns the namespace and name that is encoded in the
// domain name.
func SplitVMINamespaceKey(domainName string) (namespace, name string) {
	splitName := strings.SplitN(domainName, "_", 2)
	if len(splitName) == 1 {
		return k8sv1.NamespaceDefault, splitName[0]
	}
	return splitName[0], splitName[1]
}

// VMINamespaceKeyFunc constructs the domain name with a namespace prefix i.g.
// namespace_name.
func VMINamespaceKeyFunc(vmi *v1.VirtualMachineInstance) string {
	return DomainFromNamespaceName(vmi.Namespace, vmi.Name)
}

func DomainFromNamespaceName(namespace, name string) string {
	return fmt.Sprintf("%s_%s", namespace, name)
}

func NewDomain(dom cli.VirDomain) (*api.Domain, error) {

	name, err := dom.GetName()
	if err != nil {
		return nil, err
	}
	namespace, name := SplitVMINamespaceKey(name)

	domain := api.NewDomainReferenceFromName(namespace, name)
	domain.GetObjectMeta().SetUID(domain.Spec.Metadata.KubeVirt.UID)
	return domain, nil
}

func NewDomainFromName(name string, vmiUID types.UID) *api.Domain {
	namespace, name := SplitVMINamespaceKey(name)

	domain := api.NewDomainReferenceFromName(namespace, name)
	domain.Spec.Metadata.KubeVirt.UID = vmiUID
	domain.GetObjectMeta().SetUID(domain.Spec.Metadata.KubeVirt.UID)
	return domain
}
