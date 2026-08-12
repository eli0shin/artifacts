FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/artifact-server ./cmd/artifact-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/artifact-server /artifact-server
EXPOSE 8080
ENTRYPOINT ["/artifact-server"]
