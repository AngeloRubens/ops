# memcached without the -u root it asks for: on a kernel that says everything is root it stops,
# and on one that can say otherwise it should not have to be asked.
FROM memcached:1.6
