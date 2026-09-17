# A database whose server is threaded rather than forked.
# The configuration under test is the one the official image carries, and adding a file to it is
# what anyone deploying on top of it writes.
# mongodb 8 is built with google's tcmalloc, which stops the process when it cannot read
# /sys/devices/system/cpu/possible, and nanos lists only the processors that are online. 7 is built
# with gperftools, which does not read it.
FROM mongo:7
