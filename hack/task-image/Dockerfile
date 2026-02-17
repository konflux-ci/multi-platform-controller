FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1771346502@sha256:c7d44146f826037f6873d99da479299b889473492d3c1ab8af86f08af04ec8a0
ENV HOME=/root
RUN microdnf install -y rsync openssh-clients buildah jq

RUN curl -LO https://dl.k8s.io/release/v1.27.4/bin/linux/amd64/kubectl && \
    echo "4685bfcf732260f72fce58379e812e091557ef1dfc1bc8084226c7891dd6028f  kubectl" | sha256sum --check && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin


