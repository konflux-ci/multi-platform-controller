FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8-1782797275@sha256:463cae32c6f6f5594b11a5c22de275016bd8545ce58a6373388e8b24f13fc15c
ENV HOME=/root
RUN microdnf install -y rsync openssh-clients buildah jq

RUN curl -LO https://dl.k8s.io/release/v1.27.4/bin/linux/amd64/kubectl && \
    echo "4685bfcf732260f72fce58379e812e091557ef1dfc1bc8084226c7891dd6028f  kubectl" | sha256sum --check && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin


