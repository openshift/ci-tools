FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
LABEL maintainer="skuznets@redhat.com"

ADD slack-bot /usr/bin/slack-bot
ENTRYPOINT ["/usr/bin/slack-bot"]
