FROM registry.access.redhat.com/ubi9/ubi-minimal:9.5-1739377040@sha256:db8bf20cac0f250659f1ffbba929639dcd7e09863c20c99c5cb8f3ce8ded0497
ENV HOME=/root
RUN microdnf install -y rsync openssh-clients buildah jq

RUN curl -LO https://dl.k8s.io/release/v1.27.4/bin/linux/amd64/kubectl && \
    echo "4685bfcf732260f72fce58379e812e091557ef1dfc1bc8084226c7891dd6028f  kubectl" | sha256sum --check && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin


