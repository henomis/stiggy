# stiggy binary image.
#
# Build from the repository root:
#
#   docker build -f deploy/Dockerfile -t stiggy --build-arg VERSION=$(git describe --tags) .
FROM golang:1.26 AS build
ARG VERSION=""

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /stiggy ./cmd/stiggy

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /stiggy /stiggy
ENTRYPOINT ["/stiggy"]
