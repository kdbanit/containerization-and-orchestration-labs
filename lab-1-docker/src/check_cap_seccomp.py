
import socket
import ctypes

try:
    s = socket.socket(socket.AF_INET, socket.SOCK_RAW,
                      socket.IPPROTO_ICMP)
    print("CAP_NET_RAW: OK")
    s.close()
except OSError as e:
    print(f"CAP_NET_RAW: BLOCKED ({e})")

libc = ctypes.CDLL(None, use_errno=True)
libc.syscall.restype = ctypes.c_long

ctypes.set_errno(0)
result = libc.syscall(110)

print(f"getppid: {result}, errno: {ctypes.get_errno()}")
