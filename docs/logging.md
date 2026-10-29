# Logging

For logging use the `Log` struct provided in the `log` package (import `kubevirt.io/client-go/log`; see `staging/src/kubevirt.io/client-go/log/`).
That struct offers several functions for providing logs with different log levels, verbosity and content.

## Log Levels and Verbosity

The Log struct offers several functions for logging with a decent log level: `Info(msg)`, `Warning(msg)`, `Error(msg)`, `Critical(msg)`.
These functions also exist in a second flavor, where you can use a formatted string with additional arguments.

Additionally you can set the verbosity with `V(verbosity)`, which takes effect on info level messages.

Please check the [Kubernetes Logging Conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md) for the meaning of the different log levels and verbosity.

During runtime you will see:

- all info logs with a verbosity equal or lower to verbosity set by the `-v` command line flag
- all warning, error and critical logs

## Default values

If you don't set a verbosity for your info log statements, they will use the default value of `2`.

Also, if you don't provide a `-v` command line flag, it will use a default of `2`.

## Enhancing logs

You can enhance the log statements with some helper functions:

- `Object(o)`: `o` has to be a Kubernetes resource, this will log the name, namespace, kind and uuid of the resource
- `With(...keyvals)`: logs the given key / value pairs
- `Reason(err)`: short for `With("reason", err)`
- `Key(name, kind)`: short for `With("name", name, "kind", kind)`, where given name can be in format `namespace/name`

## OpenVMM guest serial output

OpenVMM COM1 listens on the private per-VMI `openvmm-serial0` Unix socket.
A launcher-owned broker maintains its connection independently of console users.
Guest output is appended to the existing `virt-serial0-log`, which the existing
`guest-console-log` sidecar reads with `virt-tail`. The public `virt-serial0`
socket still provides bidirectional `virtctl console` access. Management-terminal
output and OpenVMM stderr are not guest COM1 output and are not mixed into this log.

Logging follows the existing policy: `autoattachSerialConsole: false` disables
capture; otherwise an explicit `logSerialConsole` setting overrides the cluster's
`disableSerialConsoleLog` default. Disabling capture leaves interactive console
forwarding available, without opening the guest log. This does not change the
existing OpenVMM behavior of attaching COM1 even when autoattach is disabled.

### Startup and completion markers

**Startup capture is best effort, not lossless.** Dialing begins before the
OpenVMM process is started and retries every 10 ms until connected or stopped.
A connection still unavailable after five seconds is reported, but retries
continue. A successful Unix socket dial is not proof that OpenVMM has accepted
the connection or connected the UART. Output emitted before acceptance can be
lost. There is no paused-start handshake or change to the packaged OpenVMM binary.

A guest PowerShell script can write a newline-terminated
`COMPLETE RunId=<id> Result=SUCCESS` marker to COM1. Once OpenVMM is connected,
capture does not require an attached console. `virt-tail` emits complete lines;
an unterminated marker will not appear immediately in sidecar logs. The broker
copies bytes, not guest records: a guest that retransmits its whole log can emit
duplicate markers. Seeing a marker is not an acknowledgment from another service.
The existing tail follower is not an acknowledgment protocol either: its inotify
watch-registration race can delay a lone append until a later file event, even
though the bytes are already in the log. This change does not replace that
follower or promise exactly-once sidecar delivery.

### Backpressure, retention, and shutdown

Console delivery has a 16-entry queue of at most 32 KiB chunks (512 KiB plus the
in-flight write), independent of file capture. A full queue or a one-second socket
write deadline disconnects the console rather than blocking capture. A new
console replaces the previous connection and receives new output, not historical
log replay. Guest-directed console input also has a one-second write deadline.
These are intentional differences from QEMU-style blocking console backpressure.

The active log and three backups (`.0` through `.2`) each have a 2 MiB maximum,
matching the upstream virtlogd default budget, not arbitrary disk growth. New
files use the launcher's existing ownership helper so the uid-107 sidecar can
read them. Rotation can occur up to 32 KiB early at line boundaries; this reserves
room for lines up to 32 KiB, including fragmented completion markers, without
splitting them between files. Longer lines can span generations. As with other
rotating logs, a lagging sidecar can miss generations, and retention is not a
durable archive. Existing content is appended, not truncated on broker startup.

File writes are synchronous: a slow filesystem can backpressure guest output and
the live console, but does not create an unbounded in-memory log queue. An append
or rotation failure is reported and disables persistence for that VMI lifetime;
live console delivery continues. Unexpected transport termination is reported,
closes the public console, and is not automatically reconnected. Inspect
virt-launcher logs for these errors rather than treating an empty sidecar log as
proof that the guest produced no output.

After OpenVMM exits, the broker stops console input and allows up to two seconds
for buffered upstream output to reach EOF before closing the transport. It joins
its workers, syncs and closes the log before publishing the stopped domain event.
A drain deadline, sync failure, or close failure is reported. The network drain
is bounded; regular-file writes and `fsync` are not cancellable and a hung
filesystem can extend shutdown. This is not a guarantee against power loss,
forced launcher termination, upstream UART loss, or sidecar termination.