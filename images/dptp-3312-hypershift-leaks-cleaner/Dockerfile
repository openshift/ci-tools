FROM registry.ci.openshift.org/ocp/4.17:hypershift-cli
LABEL maintainer="bechen@redhat.com"

RUN yum install -y unzip jq

COPY oc /usr/bin/oc

RUN cd /tmp && \
    curl "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o "awscliv2.zip" && \
    unzip awscliv2.zip && \
    ./aws/install && \
    rm -rf awscliv2.zip aws

ADD cleanup-hypershift-leaks.sh /cleanup-hypershift-leaks.sh
ENTRYPOINT ["sh"]
CMD ["/cleanup-hypershift-leaks.sh"]
