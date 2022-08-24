FROM quay.io/centos/centos:stream9
LABEL maintainer="jguzik@redhat.com"
RUN dnf install -y nginx && dnf clean all && rm -rf /var/cache/dnf
COPY nginx.conf /etc/nginx/nginx.conf
CMD ["/usr/sbin/nginx"]
