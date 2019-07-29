## p9

p9 is a 9P2000.L client and server originally written for gVisor. gVisor is
built using bazel, so p9 is not guaranteed to be directly importable by other Go
code. This package exists to make it reusable in the Go world.

p9 also has some performance improvements to 9P2000.L specific to just *this*
client and server.
