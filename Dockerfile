# Dockerfile consumed by goreleaser. Copies the prebuilt `praxis`
# binary that goreleaser places in the build context. For from-source
# local builds use Dockerfile.dev instead.
#
# Pinned to alpine:3.21 by digest so the published image is
# reproducible against a fixed base layer. Refresh with:
#   docker buildx imagetools inspect alpine:3.21
FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

RUN adduser -D -h /home/praxis praxis
COPY praxis /usr/local/bin/

USER praxis
WORKDIR /home/praxis

EXPOSE 8080
ENTRYPOINT ["praxis"]
CMD ["serve"]
