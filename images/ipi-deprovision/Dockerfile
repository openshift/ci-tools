FROM registry.svc.ci.openshift.org/openshift/hive-v4.0:hive
LABEL maintainer="hongkliu@redhat.com"

COPY cmd/ipi-deprovision/ipi-deprovision.sh  /usr/bin/ipi-deprovision.sh

# Install awscli
RUN chmod +x /usr/bin/ipi-deprovision.sh && \
      curl -O https://bootstrap.pypa.io/get-pip.py && \
      python get-pip.py && \
      rm -fv get-pip.py && \
      pip install awscli==1.16.202

ENTRYPOINT ["/usr/bin/ipi-deprovision.sh"]
