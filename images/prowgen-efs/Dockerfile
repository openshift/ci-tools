FROM auto-config-brancher

COPY --from=us-docker.pkg.dev/k8s-infra-prow/images/checkconfig:v20261104-091a7d737 /ko-app/checkconfig /usr/bin/checkconfig
ADD prowgen-efs /usr/bin/prowgen-efs

ENTRYPOINT ["/usr/bin/prowgen-efs"]
