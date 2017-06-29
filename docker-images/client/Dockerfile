FROM alpine:3.4
MAINTAINER Shaun Crampton <shaun@tigera.io>

# Since our binary isn't designed to run as PID 1, run it via the tini init daemon.
RUN apk --no-cache add --update tini
ENTRYPOINT ["/sbin/tini", "--"]

ADD bin/typha-client /usr/bin/typha-client

# Run the client by default
CMD ["typha-client"]
