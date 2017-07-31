import socket
import sys
import time

#Set up socket and return it
def socketcreate():
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    return s

#Main server loop
#Takes addr, port and socket as arguments
#Doesn't return anything
def sendmsg(addr, port, peeraddr, peerport, s):
    s.sendto("TalkTo %s %s" % (peeraddr, peerport), (addr, port))

##### ACTIVE SECTION OF SCRIPT #####

#Set up socket
s = socketcreate()
#Send message
sendmsg(sys.argv[1], int(sys.argv[2]), sys.argv[3], int(sys.argv[4]), s)
