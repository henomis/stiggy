# stiggy binary image.
#
# Build from the repository root:
#
#   docker build -f deploy/Dockerfile -t stiggy .
FROM golang:1.26 AS build

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /stiggy ./cmd/stiggy

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /stiggy /stiggy
ENTRYPOINT ["/stiggy"]
