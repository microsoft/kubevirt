# Building KubeVirt with MSHV support

## Step 1: Clone the fork of KubeVirt with MSHV Support

```bash
git clone -b mshv-main \
    https://github.com/harshitgupta1337/kubevirt.git
```

## Step 2: Replace copy the build-kubevirt dir into Kubevirt

```bash
# Remove the existing build scripts in the KubeVirt repo
rm -rf kubevirt/hack/build-mariner-based-imgs

# Copy the build scripts from platform-tests repo
# to the KubeVirt repo
cp -r platform-tests/mshv/scripts/build-kubevirt \
    kubevirt/hack/build-mariner-based-imgs
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

token="$(az account get-access-token --query accessToken -o tsv)"

echo $token | \
    az devops login \
    --organization "https://dev.azure.com/microsoft"
```

## Step 4: Build KubeVirt

```bash
cd kubevirt/

# Build the images for hypervisor "ch"
# with access token set in Step 3
hack/build-mariner-based-imgs/build-images.sh -H ch -P $token

# Generate the Kubernetes manifest files for KubeVirt
hack/build-mariner-based-imgs/make-mariner-manifests.sh

# Push the generated images to the container registry 
# set in Step 3
hack/build-mariner-based-imgs/push-images.sh
```

