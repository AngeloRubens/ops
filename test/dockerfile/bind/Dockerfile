# bind is the most downloaded package the nanos package repository publishes, so this is the same
# server from the image its own authors ship. Two things are said here that the image leaves
# otherwise: it is asked to stay in the foreground and log to the console, which is how anything
# is read back from a unikernel, and it is not asked to drop to the bind user, there being no one
# else here to drop away from.
FROM internetsystemsconsortium/bind9:9.20
ENTRYPOINT ["/usr/sbin/named"]
CMD ["-g", "-c", "/etc/bind/named.conf"]
EXPOSE 53
