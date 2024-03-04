FROM quay.io/centos/centos:stream8
LABEL maintainer="bechen@redhat.com"
RUN echo "ARCH=$(uname -m)"

RUN yum install -y unzip jq

COPY hypershift /usr/bin/hypershift
COPY oc /usr/bin/oc

RUN cd /tmp && \
    curl "https://awscli.amazonaws.com/awscli-exe-linux-$ARCH.zip" -o "awscliv2.zip" && \
    unzip awscliv2.zip && \
    ./aws/install && \
    rm -rf awscliv2.zip aws

ADD cleanup-hypershift-leaks.sh /cleanup-hypershift-leaks.sh
ENTRYPOINT ["sh"]
CMD ["/cleanup-hypershift-leaks.sh"]
