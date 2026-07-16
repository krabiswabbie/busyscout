# ARM32 musl telnetd for BusyScout integration tests
# Runs via QEMU user-mode on non-ARM hosts
FROM arm32v7/alpine:3.20

RUN apk add --no-cache busybox-extras

RUN adduser -D user && echo "user:password" | chpasswd

EXPOSE 23
CMD ["telnetd", "-F", "-l", "/bin/login"]
