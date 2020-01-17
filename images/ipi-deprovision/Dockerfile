FROM centos:8
LABEL maintainer="hongkliu@redhat.com"

ARG JQ_VERSION=1.6

COPY usr/bin/oc /usr/bin/oc
COPY usr/bin/openshift-install /usr/bin/openshift-install

COPY cmd/ipi-deprovision/ipi-deprovision.sh  /usr/bin/ipi-deprovision.sh

COPY images/ipi-deprovision/google-cloud-sdk.repo  /etc/yum.repos.d/google-cloud-sdk.repo

RUN chmod +x /usr/bin/ipi-deprovision.sh && \
      dnf install --nogpg -y google-cloud-sdk python3 jq less && \
      dnf clean all && \
      curl -O https://bootstrap.pypa.io/get-pip.py && \
      python3 get-pip.py && \
      rm -fv get-pip.py && \
      pip install awscli

ENTRYPOINT ["/usr/bin/ipi-deprovision.sh"]
