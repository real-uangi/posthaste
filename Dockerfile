FROM gcr.io/distroless/static-debian13:nonroot

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/posthaste /usr/local/bin/posthaste

ENV RUN_MODE=release HTTP_PORT=8080
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/posthaste"]
