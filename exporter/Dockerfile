FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -o /queue-exporter .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /queue-exporter /queue-exporter
USER nonroot:nonroot
ENTRYPOINT ["/queue-exporter"]
