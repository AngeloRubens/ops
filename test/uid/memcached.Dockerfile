# memcached without the -u root it asks for, and with -v so that it says when it is up: on a
# kernel that says everything is root it stops, and on one that can say otherwise it should not
# have to be asked.
FROM memcached:1.6
CMD ["memcached", "-v"]
