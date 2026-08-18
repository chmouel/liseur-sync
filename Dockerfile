FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# templ CLI for codegen (build-stage only; not in the final image)
RUN go install github.com/a-h/templ/cmd/templ@latest
COPY . .
RUN templ generate ./internal/webui/ \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/liseur-sync ./cmd/liseur-sync

FROM scratch
# non-root; tzdata is embedded in the binary (time/tzdata)
USER 65532:65532
ENV LISEUR_CACHE_DIR=/data/cache
COPY --from=build /out/liseur-sync /liseur-sync
EXPOSE 8585
ENTRYPOINT ["/liseur-sync"]
CMD ["serve", "-config", "/data/liseur-sync.toml"]
