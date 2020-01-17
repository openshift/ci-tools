FROM centos:8

RUN dnf install -y nodejs && \
    npm install -g @bitwarden/cli
ADD ci-secret-bootstrap /usr/bin/ci-secret-bootstrap
ENTRYPOINT ["/usr/bin/ci-secret-bootstrap"]
