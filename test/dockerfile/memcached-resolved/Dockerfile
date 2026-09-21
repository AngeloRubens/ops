# memcached declines to start as root unless told to with -u root, and a released nanos answers
# root to whoever asks. -vv has it say when it is listening.
# The debian image rather than the alpine one: the same program, asked for in the same way, booted
# once on a kernel that reads uid from the manifest, while this scenario on alpine says nothing at
# all and is killed where it stands. One thing at a time, so only the base changes.
FROM memcached:1.6
CMD ["memcached", "-u", "root", "-vv"]
