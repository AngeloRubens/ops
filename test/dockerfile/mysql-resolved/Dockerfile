# The official mysql image, told --user=root: mysqld will not run as root otherwise, and a released
# nanos answers root to whoever asks.
FROM mysql:8.4
ENV MYSQL_ALLOW_EMPTY_PASSWORD=1
CMD ["mysqld", "--user=root"]
