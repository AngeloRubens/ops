# The official postgres image, which declares USER root and hands over to the postgres user
# through gosu inside its entrypoint - so what it runs as is only knowable by running it.
FROM postgres:18
ENV POSTGRES_PASSWORD=survey
