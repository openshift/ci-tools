FROM registry.svc.ci.openshift.org/openshift/hive-v4.0:hive
LABEL maintainer="hongkliu@redhat.com"

ARG JQ_VERSION=1.6

COPY cmd/ipi-deprovision/ipi-deprovision.sh  /usr/bin/ipi-deprovision.sh

# Install awscli
RUN chmod +x /usr/bin/ipi-deprovision.sh && \
      curl -O https://bootstrap.pypa.io/get-pip.py && \
      python get-pip.py && \
      rm -fv get-pip.py && \
      pip install awscli==1.16.202 && \
      curl -L -o jq "https://github.com/stedolan/jq/releases/download/jq-${JQ_VERSION}/jq-linux64" && \
      chmod +x ./jq && \
      mv ./jq /usr/bin && \
      yum install -y less && \
      yum clean all && \
      rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/ipi-deprovision.sh"]
