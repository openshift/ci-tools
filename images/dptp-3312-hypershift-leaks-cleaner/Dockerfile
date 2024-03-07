FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="bechen@redhat.com"

RUN microdnf install -y unzip jq

COPY oc /usr/bin/oc
COPY hypershift /usr/bin/hypershift

RUN cd /tmp && \
    curl "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o "awscliv2.zip" && \
    unzip awscliv2.zip && \
    ./aws/install && \
    rm -rf awscliv2.zip aws

ADD cleanup-hypershift-leaks.sh /cleanup-hypershift-leaks.sh
ENTRYPOINT ["sh"]
CMD ["/cleanup-hypershift-leaks.sh"]
