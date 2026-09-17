# A database whose server is threaded rather than forked.
# The configuration under test is the one the official image carries, and adding a file to it is
# what anyone deploying on top of it writes.
# mongodb counts the processors of the machine before it does anything else, and stops when the
# answer is none: 7 fails an invariant that wants more than none, and 8, built with google's
# tcmalloc, reads /sys/devices/system/cpu/possible first and stops there. The package carries the
# files that answer both.
FROM mongo:8
