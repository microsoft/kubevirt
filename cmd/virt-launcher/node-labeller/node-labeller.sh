#!/bin/bash

set -xeo pipefail

# Default values for env vars, can be overridden by user input
KVM_HYPERVISOR_DEVICE="kvm"
KVM_VIRTTYPE="kvm"

NODE_LABELLER_DIR="/var/lib/kubevirt-node-labeller"
mkdir -p "$NODE_LABELLER_DIR"

cat > "$NODE_LABELLER_DIR/capabilities.xml" <<'EOF'
<capabilities>
	<host>
		<cpu/>
	</host>
</capabilities>
EOF

cat > "$NODE_LABELLER_DIR/supported_features.xml" <<'EOF'
<cpu/>
EOF

cat > "$NODE_LABELLER_DIR/virsh_domcapabilities.xml" <<'EOF'
<domainCapabilities>
	<cpu/>
</domainCapabilities>
EOF
