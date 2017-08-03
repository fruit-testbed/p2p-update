import socket
import sys
import time

#Usage: python talkto.py (addr-of-proxy) (port-used-by-proxy) (addr-of-peer)

#Set up socket and return it
def socketcreate():
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    return s

#Main server loop
#Takes addr of server, port used by server, public-facing addr of peer, socket as arguments
#Doesn't return anything
def sendmsg(addr, port, peeraddr, s):
    s.sendto("TalkTo %s" % peeraddr, (addr, port))

##### ACTIVE SECTION OF SCRIPT #####

#Set up socket
s = socketcreate()
#Send message
sendmsg(sys.argv[1], int(sys.argv[2]), sys.argv[3], s)
