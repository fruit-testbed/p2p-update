import socket
import sys
import time

#Usage: python endsession.py (addr-of-peer) (port-used-by-peer) (own-addr)

#Set up socket and return it
def socketcreate():
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    return s

#Main server loop
#Takes addr, port and socket as arguments
#Doesn't return anything
def sendmsg(addr, port, s):
    s.sendto("TerminateSession %s" % sys.argv[3], (addr, port))

##### ACTIVE SECTION OF SCRIPT #####

#Set up socket
s = socketcreate()
#Send message
sendmsg(sys.argv[1], int(sys.argv[2]), s)
