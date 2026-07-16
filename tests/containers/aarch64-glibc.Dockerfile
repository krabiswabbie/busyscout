# aarch64 glibc telnetd for BusyScout integration tests
# Runs natively on Apple Silicon (arm64)
FROM arm64v8/ubuntu:22.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    telnetd xinetd \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m user && echo "user:password" | chpasswd

RUN echo "telnet stream tcp nowait root /usr/sbin/telnetd telnetd" > /etc/xinetd.d/telnet \
    && echo "disable = no" >> /etc/xinetd.d/telnet

EXPOSE 23
CMD ["xinetd", "-dontfork"]
