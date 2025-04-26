package eventsclient

import (
	"fmt"
	"time"

	"kubevirt.io/kubevirt/pkg/virt-launcher/metadata"

	"libvirt.org/go/libvirt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/virt-launcher-common/api"
	eventsClientCommon "kubevirt.io/kubevirt/pkg/virt-launcher-common/notify-client"
	agentpoller "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/agent-poller"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/cli"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/converter"
	domainerrors "kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/errors"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/util"
)

const (
	cantDetermineLibvirtDomainName = "Could not determine name of libvirt domain in event callback."
	libvirtEventChannelFull        = "Libvirt event channel is full, dropping event."
)

// TODO PLUGINDEV: Do we need to define this struct in the virt-launcher-common pkg and then create subclasses for individual virt-launchers?
type libvirtEvent struct {
	Domain     string
	Event      *libvirt.DomainEventLifecycle
	AgentEvent *libvirt.DomainEventAgentLifecycle
}

func newWatchEventError(err error) watch.Event {
	return watch.Event{Type: watch.Error, Object: &metav1.Status{Status: metav1.StatusFailure, Message: err.Error()}}
}

type eventCaller struct {
	domainStatus             api.LifeCycle
	domainStatusChangeReason api.StateChangeReason
}

func (e *eventCaller) printStatus(status *api.DomainStatus) {
	v := 2
	if status.Status == e.domainStatus && status.Reason == e.domainStatusChangeReason {
		// Status hasn't changed so log only in higher verbosity.
		v = 3
	}
	log.Log.V(v).Infof("kubevirt domain status: %v(%v) reason: %v(%v)", status.Status, e.domainStatus, status.Reason, e.domainStatusChangeReason)
}

func (e *eventCaller) updateStatus(status *api.DomainStatus) {
	e.domainStatus = status.Status
	e.domainStatusChangeReason = status.Reason
}

func (e *eventCaller) eventCallback(c cli.Connection, domain *api.Domain, libvirtEvent libvirtEvent, client *eventsClientCommon.NotifyClient, events chan watch.Event,
	interfaceStatus []api.InterfaceStatus, osInfo *api.GuestOSInfo, vmi *v1.VirtualMachineInstance, fsFreezeStatus *api.FSFreeze,
	metadataCache *metadata.Cache) {
	// TODO PLUGINDEV: LookupDomainByName returns a Libvirt object.
	// TODO PLUGINDEV: Later, the state/reason of the api.Domain is set by converting the Libvirt state/reason
	d, err := c.LookupDomainByName(util.DomainFromNamespaceName(domain.ObjectMeta.Namespace, domain.ObjectMeta.Name))
	if err != nil {
		if !domainerrors.IsNotFound(err) {
			log.Log.Reason(err).Error("Could not fetch the Domain.")
			return
		}
		domain.SetState(api.NoState, api.ReasonNonExistent)
	} else {
		defer d.Free()

		// No matter which event, try to fetch the domain xml
		// and the state. If we get a IsNotFound error, that
		// means that the VirtualMachineInstance was removed.
		status, reason, err := d.GetState()
		if err != nil {
			if !domainerrors.IsNotFound(err) {
				log.Log.Reason(err).Error("Could not fetch the Domain state.")
				return
			}
			domain.SetState(api.NoState, api.ReasonNonExistent)
		} else {
			domain.SetState(util.ConvState(status), util.ConvReason(status, reason))
		}

		kubevirtMetadata := metadata.LoadKubevirtMetadata(metadataCache)
		// TODO PLUGINDEV: Getting the Domain XML from Libvirt and using it to set the api.Domain.Spec field.
		spec, err := util.GetDomainSpecWithRuntimeInfo(d)
		if err != nil {
			// NOTE: Getting domain metadata for a live-migrating VM isn't allowed
			if !domainerrors.IsNotFound(err) && !domainerrors.IsInvalidOperation(err) {
				log.Log.Reason(err).Error("Could not fetch the Domain specification.")
				return
			}
		} else {
			domain.ObjectMeta.UID = kubevirtMetadata.UID
		}

		if spec != nil {
			spec.Metadata.KubeVirt = kubevirtMetadata
			domain.Spec = *spec // TODO PLUGINDEV: Here, we should be converting from virt-stack-specific spec to api.Domain.Spec
		}

		e.printStatus(&domain.Status)
		e.updateStatus(&domain.Status)
	}

	// TODO PLUGINDEV: By this point, the virtstack-specific code should have converted the cli.VirDomain to api.Domain.
	// TODO PLUGINDEV: Now they just need to send the event.

	switch domain.Status.Reason { // TODO PLUGINDEV: This could be changed to check virtstack-specific Status/Reason
	case api.ReasonNonExistent:
		now := metav1.Now()
		domain.ObjectMeta.DeletionTimestamp = &now
		watchEvent := watch.Event{Type: watch.Modified, Object: domain}
		client.SendDomainEvent(watchEvent)
		updateEvents(watchEvent, domain, events)
	case api.ReasonPausedIOError:
		domainDisksWithErrors, err := d.GetDiskErrors(0)
		if err != nil {
			log.Log.Reason(err).Error("Could not get disks with errors")
		}
		for _, disk := range domainDisksWithErrors {
			volumeName := converter.GetVolumeNameByTarget(domain, disk.Disk)
			var reasonError string
			switch disk.Error {
			case libvirt.DOMAIN_DISK_ERROR_NONE:
				continue
			case libvirt.DOMAIN_DISK_ERROR_UNSPEC:
				reasonError = fmt.Sprintf("VM Paused due to IO error at the volume: %s", volumeName)
			case libvirt.DOMAIN_DISK_ERROR_NO_SPACE:
				reasonError = fmt.Sprintf("VM Paused due to not enough space on volume: %s", volumeName)
			}
			err = client.SendK8sEvent(vmi, "Warning", "IOerror", reasonError)
			if err != nil {
				log.Log.Reason(err).Error(fmt.Sprintf("Could not send k8s event"))
			}
			event := watch.Event{Type: watch.Modified, Object: domain}
			client.SendDomainEvent(event)
			updateEvents(event, domain, events)
		}
	default:
		if libvirtEvent.Event != nil {
			if libvirtEvent.Event.Event == libvirt.DOMAIN_EVENT_DEFINED && libvirt.DomainEventDefinedDetailType(libvirtEvent.Event.Detail) == libvirt.DOMAIN_EVENT_DEFINED_ADDED {
				event := watch.Event{Type: watch.Added, Object: domain}
				client.SendDomainEvent(event)
				updateEvents(event, domain, events)
			} else if libvirtEvent.Event.Event == libvirt.DOMAIN_EVENT_STARTED && libvirt.DomainEventStartedDetailType(libvirtEvent.Event.Detail) == libvirt.DOMAIN_EVENT_STARTED_MIGRATED {
				event := watch.Event{Type: watch.Added, Object: domain}
				client.SendDomainEvent(event)
				updateEvents(event, domain, events)
			}
		}
		if interfaceStatus != nil {
			domain.Status.Interfaces = interfaceStatus
		}
		if osInfo != nil {
			domain.Status.OSInfo = *osInfo
		}

		if fsFreezeStatus != nil {
			domain.Status.FSFreezeStatus = *fsFreezeStatus
		}

		err := client.SendDomainEvent(watch.Event{Type: watch.Modified, Object: domain})
		if err != nil {
			log.Log.Reason(err).Error("Could not send domain notify event.")
		}
	}
}

var updateEvents = updateEventsClosure()

func updateEventsClosure() func(event watch.Event, domain *api.Domain, events chan watch.Event) {
	firstAddEvent := true
	firstDeleteEvent := true

	return func(event watch.Event, domain *api.Domain, events chan watch.Event) {
		if event.Type == watch.Added && firstAddEvent {
			firstAddEvent = false
			events <- event
		} else if event.Type == watch.Modified && domain.ObjectMeta.DeletionTimestamp != nil && firstDeleteEvent {
			firstDeleteEvent = false
			events <- event
		}
	}
}

func StartLibvirtNotifier(
	notifier *eventsClientCommon.NotifyClient,
	domainConn cli.Connection,
	deleteNotificationSent chan watch.Event,
	vmi *v1.VirtualMachineInstance,
	domainName string,
	agentStore *agentpoller.AsyncAgentStore,
	qemuAgentSysInterval time.Duration,
	qemuAgentFileInterval time.Duration,
	qemuAgentUserInterval time.Duration,
	qemuAgentVersionInterval time.Duration,
	qemuAgentFSFreezeStatusInterval time.Duration,
	metadataCache *metadata.Cache,
) error {

	eventChan := make(chan libvirtEvent, 10)

	reconnectChan := make(chan bool, 10)

	var domainCache *api.Domain

	domainConn.SetReconnectChan(reconnectChan)

	agentPoller := agentpoller.CreatePoller(
		domainConn,
		vmi.UID,
		domainName,
		agentStore,
		qemuAgentSysInterval,
		qemuAgentFileInterval,
		qemuAgentUserInterval,
		qemuAgentVersionInterval,
		qemuAgentFSFreezeStatusInterval,
	)

	// Run the event process logic in a separate go-routine to not block libvirt
	go func() {
		var interfaceStatuses []api.InterfaceStatus
		var guestOsInfo *api.GuestOSInfo
		var fsFreezeStatus *api.FSFreeze
		var eventCaller eventCaller

		for {
			select {
			// TODO PLUGINDEV: eventChan receives all the callbacks registered against LibVirt using its API Calls like Register....
			// That is why the event is passed as it is to eventCallback
			case event := <-eventChan:
				metadataCache.ResetNotification()
				domainCache = util.NewDomainFromName(event.Domain, vmi.UID)
				eventCaller.eventCallback(domainConn, domainCache, event, notifier, deleteNotificationSent, interfaceStatuses, guestOsInfo, vmi, fsFreezeStatus, metadataCache)
				log.Log.Infof("Domain name event: %v", domainCache.Spec.Name)
				if event.AgentEvent != nil {
					if event.AgentEvent.State == libvirt.CONNECT_DOMAIN_EVENT_AGENT_LIFECYCLE_STATE_CONNECTED {
						agentPoller.Start()
					} else if event.AgentEvent.State == libvirt.CONNECT_DOMAIN_EVENT_AGENT_LIFECYCLE_STATE_DISCONNECTED {
						agentPoller.Stop()
					}
				}
				// TODO PLUGINDEV: AgentUpdated is written to whenever AgentPoller calls Store fn to save some info
			case agentUpdate := <-agentStore.AgentUpdated:
				metadataCache.ResetNotification()
				interfaceStatuses = agentUpdate.DomainInfo.Interfaces
				guestOsInfo = agentUpdate.DomainInfo.OSInfo
				fsFreezeStatus = agentUpdate.DomainInfo.FSFreezeStatus

				eventCaller.eventCallback(domainConn, domainCache, libvirtEvent{}, notifier, deleteNotificationSent,
					interfaceStatuses, guestOsInfo, vmi, fsFreezeStatus, metadataCache)
			case <-reconnectChan:
				// TODO PLUGINDEV: Directly sending the DomainEvent
				notifier.SendDomainEvent(newWatchEventError(fmt.Errorf("Libvirt reconnect, domain %s", domainName)))

			case <-metadataCache.Listen():
				// Metadata cache updates should be processed only *after* at least one
				// libvirt event arrived (which creates the first domainCache).
				if domainCache != nil {
					domainCache = util.NewDomainFromName(
						util.DomainFromNamespaceName(domainCache.ObjectMeta.Namespace, domainCache.ObjectMeta.Name),
						vmi.UID,
					)
					eventCaller.eventCallback(
						domainConn,
						domainCache,
						libvirtEvent{},
						notifier,
						deleteNotificationSent,
						interfaceStatuses,
						guestOsInfo,
						vmi,
						fsFreezeStatus,
						metadataCache,
					)
				}
			}
		}
	}()

	domainEventLifecycleCallback := func(c *libvirt.Connect, d *libvirt.Domain, event *libvirt.DomainEventLifecycle) {

		log.Log.Infof("DomainLifecycle event %s with event id %d reason %d received", event.String(), event.Event, event.Detail)
		name, err := d.GetName()
		if err != nil {
			log.Log.Reason(err).Info(cantDetermineLibvirtDomainName)
		}
		select {
		case eventChan <- libvirtEvent{Event: event, Domain: name}:
		default:
			log.Log.Infof(libvirtEventChannelFull)
		}
	}

	domainEventDeviceAddedCallback := func(c *libvirt.Connect, d *libvirt.Domain, event *libvirt.DomainEventDeviceAdded) {
		log.Log.Infof("Domain Device Added event received")
		name, err := d.GetName()
		if err != nil {
			log.Log.Reason(err).Info(cantDetermineLibvirtDomainName)
		}
		select {
		case eventChan <- libvirtEvent{Domain: name}:
		default:
			log.Log.Infof(libvirtEventChannelFull)
		}
	}

	domainEventDeviceRemovedCallback := func(c *libvirt.Connect, d *libvirt.Domain, event *libvirt.DomainEventDeviceRemoved) {
		log.Log.Infof("Domain Device Removed event received")
		name, err := d.GetName()
		if err != nil {
			log.Log.Reason(err).Info(cantDetermineLibvirtDomainName)
		}

		select {
		case eventChan <- libvirtEvent{Domain: name}:
		default:
			log.Log.Infof(libvirtEventChannelFull)
		}
	}

	domainEventMemoryDeviceSizeChange := func(c *libvirt.Connect, d *libvirt.Domain, event *libvirt.DomainEventMemoryDeviceSizeChange) {
		log.Log.Infof("Domain Memory Device size-change event received")
		name, err := d.GetName()
		if err != nil {
			log.Log.Reason(err).Info(cantDetermineLibvirtDomainName)
		}

		select {
		case eventChan <- libvirtEvent{Domain: name}:
		default:
			log.Log.Infof(libvirtEventChannelFull)
		}
	}

	err := domainConn.DomainEventLifecycleRegister(domainEventLifecycleCallback)
	if err != nil {
		log.Log.Reason(err).Errorf("failed to register event callback with libvirt")
		return err
	}

	err = domainConn.DomainEventDeviceAddedRegister(domainEventDeviceAddedCallback)
	if err != nil {
		log.Log.Reason(err).Errorf("failed to register device added event callback with libvirt")
		return err
	}
	err = domainConn.DomainEventDeviceRemovedRegister(domainEventDeviceRemovedCallback)
	if err != nil {
		log.Log.Reason(err).Errorf("failed to register device removed event callback with libvirt")
		return err
	}
	err = domainConn.DomainEventMemoryDeviceSizeChangeRegister(domainEventMemoryDeviceSizeChange)
	if err != nil {
		log.Log.Reason(err).Errorf("failed to register memory device size change event callback with libvirt")
		return err
	}

	agentEventLifecycleCallback := func(c *libvirt.Connect, d *libvirt.Domain, event *libvirt.DomainEventAgentLifecycle) {
		log.Log.Infof("GuestAgentLifecycle event state %d with reason %d received", event.State, event.Reason)
		name, err := d.GetName()
		if err != nil {
			log.Log.Reason(err).Info(cantDetermineLibvirtDomainName)
		}
		select {
		case eventChan <- libvirtEvent{AgentEvent: event, Domain: name}:
		default:
			log.Log.Infof(libvirtEventChannelFull)
		}
	}
	err = domainConn.AgentEventLifecycleRegister(agentEventLifecycleCallback)
	if err != nil {
		log.Log.Reason(err).Errorf("failed to register event callback with libvirt")
		return err
	}

	log.Log.Infof("Registered libvirt event notify callback")
	return nil
}
