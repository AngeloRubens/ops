# The official nginx image, told to run as the one process it is on nanos: there is no fork for a
# master to start its workers with. That is also a command line nginx does not rewrite, so what the
# image starts can be read.
FROM nginx:1.29-alpine
COPY index.html /usr/share/nginx/html/index.html
CMD ["nginx", "-g", "daemon off; master_process off;"]
