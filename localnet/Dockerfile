# Keystone localnet image (spec M2-T1). Build context: the repository root.
# The binaries are cross-compiled on the host by `make localnet-bin` (GOOS=linux,
# CGO_ENABLED=0) and copied in, so the image build needs no network access:
#   keystoned
#   keystoned-byz  -tags byzantine   (adversarial proposer, never in releases)
#   mockfeed
FROM debian:bookworm-slim
COPY build/linux/keystoned build/linux/keystoned-byz build/linux/mockfeed /usr/local/bin/
WORKDIR /home/keystone
