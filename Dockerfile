# Dockerfile consumed by goreleaser. Copies the prebuilt `praxis`
# binary that goreleaser places in the build context. For from-source
# local builds use Dockerfile.dev instead.
FROM alpine:3.21

RUN adduser -D -h /home/praxis praxis
COPY praxis /usr/local/bin/

USER praxis
WORKDIR /home/praxis

EXPOSE 8080
ENTRYPOINT ["praxis"]
CMD ["serve"]
