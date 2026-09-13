# A datastore that writes a write ahead log and fsyncs it continuously, which is the hardest use
# the writable file system gets anywhere in this suite. Its command is an exec form.
FROM gcr.io/etcd-development/etcd:v3.5.17
EXPOSE 2379
CMD ["/usr/local/bin/etcd", \
     "--data-dir", "/etcd-data", \
     "--listen-client-urls", "http://0.0.0.0:2379", \
     "--advertise-client-urls", "http://127.0.0.1:2379"]
