import os
import sys
import socket
import time

##### FUNCTION DEFINITIONS #####

def getnatinfo(addr, port):
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    localaddress = s.getsockname()
    s.sendto("My locally detected IP is %s " % localaddress[0], (addr, port))
    print "Initial recv: %s" % s.recv(4096)
    #Receive public-facing IP address from server
    #(message only (arg[0]), strip address (arg[1]))
    external_ip = s.recvfrom(4096)[0]
    print external_ip
    #Receive external port
    external_port = s.recvfrom(4096)[0]
    print external_port
    time.sleep(1)
    peerlist = s.recvfrom(4096)[0]
    print "Peerlist: %s" % str(peerlist)
    s.close()

###### ACTIVE SECTION OF SCRIPT #####

#Arguments: address, port
getnatinfo(sys.argv[1], int(sys.argv[2]))
