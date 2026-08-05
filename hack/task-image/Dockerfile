FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8-1785906621@sha256:dd334afa72444fa46238fcf9e6bd399245adf746378735348cf84b9dfdca38f1
ENV HOME=/root
RUN microdnf install -y rsync openssh-clients buildah jq

RUN curl -LO https://dl.k8s.io/release/v1.27.4/bin/linux/amd64/kubectl && \
    echo "4685bfcf732260f72fce58379e812e091557ef1dfc1bc8084226c7891dd6028f  kubectl" | sha256sum --check && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin


