FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8-1784705586@sha256:2e8edce823a48e51858f1fad3ff4cbf6875ce8a3f86b9eecf298bc2050c8652a
ENV HOME=/root
RUN microdnf install -y rsync openssh-clients buildah jq

RUN curl -LO https://dl.k8s.io/release/v1.27.4/bin/linux/amd64/kubectl && \
    echo "4685bfcf732260f72fce58379e812e091557ef1dfc1bc8084226c7891dd6028f  kubectl" | sha256sum --check && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin


