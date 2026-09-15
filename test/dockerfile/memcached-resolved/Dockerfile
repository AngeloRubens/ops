# memcached declines to start as root unless told to with -u root, and a released nanos answers
# root to whoever asks. -vv has it say when it is listening.
FROM memcached:1.6-alpine
CMD ["memcached", "-u", "root", "-vv"]
