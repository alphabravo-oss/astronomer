# Local CI uses the official GitHub Actions runner, pinned to the multi-platform
# manifest resolved on 2026-09-16. The upstream image intentionally omits the
# hosted Ubuntu build toolchain that Astronomer's Go/race and Python gates need.
FROM ghcr.io/actions/actions-runner@sha256:e5496277be5d09bc968b3d64911b74e219ac4a3f2edce956a3ecf9271bea1ef4

RUN sudo apt-get update \
 && sudo apt-get install -y --no-install-recommends \
      build-essential \
      python3 \
      ripgrep \
      shellcheck \
      xz-utils \
 && sudo rm -rf /var/lib/apt/lists/* \
 && sudo git config --system --add safe.directory /home/runner/_work/astronomer/astronomer \
 # Local CI replaces /usr/bin/git with its checkout shim and preserves the
 # real executable as git.real. Keep Git's server-side plumbing on the real
 # binary so local-path clones and pushes used by go-git tests still work.
 && sudo ln -sf git.real /usr/bin/git-receive-pack \
 && sudo ln -sf git.real /usr/bin/git-upload-archive \
 && sudo ln -sf git.real /usr/bin/git-upload-pack

ARG HELM_VERSION=v3.21.0
ARG K3D_VERSION=v5.8.3
ARG KUBECTL_VERSION=v1.35.2
RUN set -eux; \
    architecture="$(dpkg --print-architecture)"; \
    case "$architecture" in \
      amd64) binary_arch=amd64 ;; \
      arm64) binary_arch=arm64 ;; \
      *) echo "unsupported Local CI architecture: $architecture" >&2; exit 2 ;; \
    esac; \
    helm_archive="helm-${HELM_VERSION}-linux-${binary_arch}.tar.gz"; \
    curl -fsSLo "/tmp/${helm_archive}" "https://get.helm.sh/${helm_archive}"; \
    curl -fsSLo /tmp/helm.sha256 "https://get.helm.sh/helm-${HELM_VERSION}-linux-${binary_arch}.tar.gz.sha256sum"; \
    (cd /tmp && sha256sum --check helm.sha256); \
    tar -xzf "/tmp/${helm_archive}" -C /tmp; \
    sudo install -m 0755 "/tmp/linux-${binary_arch}/helm" /usr/local/bin/helm; \
    curl -fsSLo /tmp/k3d "https://github.com/k3d-io/k3d/releases/download/${K3D_VERSION}/k3d-linux-${binary_arch}"; \
    curl -fsSLo /tmp/k3d-checksums.txt "https://github.com/k3d-io/k3d/releases/download/${K3D_VERSION}/checksums.txt"; \
    awk -v asset="_dist/k3d-linux-${binary_arch}" '$2 == asset {print $1 "  /tmp/k3d"}' /tmp/k3d-checksums.txt | sha256sum --check; \
    sudo install -m 0755 /tmp/k3d /usr/local/bin/k3d; \
    curl -fsSLo /tmp/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${binary_arch}/kubectl"; \
    curl -fsSLo /tmp/kubectl.sha256 "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${binary_arch}/kubectl.sha256"; \
    printf '%s  %s\n' "$(cat /tmp/kubectl.sha256)" /tmp/kubectl | sha256sum --check; \
    sudo install -m 0755 /tmp/kubectl /usr/local/bin/kubectl; \
    rm -rf "/tmp/${helm_archive}" /tmp/helm.sha256 /tmp/linux-* /tmp/k3d /tmp/k3d-checksums.txt /tmp/kubectl /tmp/kubectl.sha256
