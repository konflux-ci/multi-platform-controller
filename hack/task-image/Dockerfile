FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8-1779709832@sha256:457cb288730d29e2a59823b3b56341466fa94f3158e399c5ab2c4409fee0c562
ENV HOME=/root
RUN microdnf install -y rsync openssh-clients buildah jq

RUN curl -LO https://dl.k8s.io/release/v1.27.4/bin/linux/amd64/kubectl && \
    echo "4685bfcf732260f72fce58379e812e091557ef1dfc1bc8084226c7891dd6028f  kubectl" | sha256sum --check && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin


