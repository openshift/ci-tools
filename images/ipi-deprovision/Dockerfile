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
      curl -L -O https://github.com/openshift/origin/releases/download/v3.11.0/openshift-origin-client-tools-v3.11.0-0cbc58b-linux-64bit.tar.gz && \
      tar -xzf openshift-origin-client-tools-v3.11.0-0cbc58b-linux-64bit.tar.gz && \
      mv openshift-origin-client-tools-v3.11.0-0cbc58b-linux-64bit/{oc,kubectl} /usr/bin/ && \
      rm -rfv ./openshift-origin-client-tools* && \
      yum install -y less && \
      yum clean all && \
      rm -rf /var/cache/yum

ENTRYPOINT ["/usr/bin/ipi-deprovision.sh"]
