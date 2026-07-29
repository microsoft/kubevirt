# Building KubeVirt with MSHV support

## Step 1: Clone the fork of KubeVirt with MSHV Support

```bash
git clone -b mshv-main \
    https://github.com/microsoft/kubevirt.git

# Setting the path to KubeVirt repo to refer later in this doc
KUBEVIRT_ROOT=$(realpath kubevirt)

```

## Step 2: Copy the build-kubevirt scripts into Kubevirt

```bash
# Clone the platform-tests repo
git clone https://mariner-org@dev.azure.com/mariner-org/ECF/_git/platform-tests

# Copy the build scripts from platform-tests repo
# to the KubeVirt repo
cp -r platform-tests/mshv/scripts/build-kubevirt \
    $KUBEVIRT_ROOT/hack/build-mariner-based-imgs
```

## Step 3: Setup Env Vars and Azure DevOps PAT

```bash
# Set env vars for the built images
export DOCKER_PREFIX=acrafoimages.azurecr.io/kubevirt-mshv
export DOCKER_TAG=<tag>

# [Optional] If only specific KubeVirt containers are to be
# built, specify the KUBEVIRT_COMPONENTS env var
# If not set, the script will build all components
export KUBEVIRT_COMPONENTS="virt-launcher virt-handler"

# Login to Azure AAD, get token and
# login to Azure DevOps org microsoft
az login --use-device-code

# Login to the Azure Container Registry referred to by $DOCKER_PREFIX variable 
az acr login -n acrafoimages

```

## Step 4: Download LSG RPMs for CH/MSHV-based virt-launcher (skip for QEMU)

We need latest libvirt and cloud-hypervisor RPMs from LSG to build 
the virt-launcher image that runs cloud-hypervisor. 
In this step, we will download them from the `lsg-virt` artifact feed. 

Note: If you're building a QEMU-based KubeVirt distribution, this step is optional.

```bash
cd $KUBEVIRT_ROOT

# Create the directory to store downloaded RPMs.
lsgRpmsDir=./lsgRpms
mkdir -p $lsgRpmsDir

# download the latest version of dom0-feed-pkg-info-3.0 package.
# It points to the latest (pre-release) version of all packages
# published by LSG's pipelines.
az artifacts universal download \
    --organization "https://microsoft.visualstudio.com/" \
    --feed "lsg-virt" \
    --name "dom0-feed-pkg-info-3.0" \
    --version "*" \
    --path .

rpmPkgName=dom0-rpm-x86_64-dev
rpmPkgLatestVersion=$(cat packages.json | jq -r ".\"${rpmPkgName}\".version")

az artifacts universal download \
    --organization "https://microsoft.visualstudio.com/" \
    --feed "lsg-virt" \
    --name "${rpmPkgName}" \
    --version "${rpmPkgLatestVersion}" \
    --path $lsgRpmsDir
```

## Step 5: Build KubeVirt

```bash
cd $KUBEVIRT_ROOT

# Option1: Building the images for Cloud-Hypervisor("ch")-based KubeVirt
# with the directory containing LSG RPMs
hack/build-mariner-based-imgs/build-images.sh -H ch -R $lsgRpmsDir/x86_64/

# Option2: Otherwise, if building images for "qemu"-based KubeVirt
hack/build-mariner-based-imgs/build-images.sh -H qemu

# Generate the Kubernetes manifest files for KubeVirt
hack/build-mariner-based-imgs/make-mariner-manifests.sh

# Push the generated images to the container registry 
# set in Step 3
hack/build-mariner-based-imgs/push-images.sh
```

