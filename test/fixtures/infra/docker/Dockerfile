FROM golang:1.22 AS builder
WORKDIR /src
COPY app/ .
RUN go build -o /out/api .

FROM gcr.io/distroless/static:nonroot
ENV API_KEY=CONCORDIA-SEED-dockerfile-env-2b8c
ENV LISTEN_ADDR=:8080
COPY --from=builder /out/api /usr/local/bin/api
COPY --from=ghcr.io/example/healthcheck:1.0.0 /healthcheck /usr/local/bin/healthcheck
ENTRYPOINT ["/usr/local/bin/api"]
