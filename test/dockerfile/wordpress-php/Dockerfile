# WordPress served by the web server php carries, which is one process and makes no others. It is
# the shape the wordpress package in the ops repository is built in, and the one a unikernel can
# run: the official image puts apache in front of it in prefork, whose workers are made with fork.
FROM php:8.3-cli
ADD https://wordpress.org/wordpress-6.6.1.tar.gz /tmp/wordpress.tar.gz
RUN tar -xzf /tmp/wordpress.tar.gz -C /usr/src && rm /tmp/wordpress.tar.gz
WORKDIR /usr/src/wordpress
EXPOSE 8080
CMD ["php", "-S", "0.0.0.0:8080"]
