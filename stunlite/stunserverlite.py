import socket
import sys
import time

#Set up socket and return it
def socketcreate(host, port):
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind((host, port))
    return s


#Adds a peer to list of recent connections if not already present
#Doesn't return anything
def addpeer(addr, port, list):
    if [addr, port] not in list:
        list.append([addr, port])
    print list


#Main server loop
#Takes a socket as argument 
#Doesn't return anything
def serverloop(s, list):
    #Receive data and address
    data, clientaddr = s.recvfrom(4096)
    print "Received message from %s on port %s" % (clientaddr[0], clientaddr[1])
    print data
    s.sendto("Message received\n", clientaddr)
    #Send received external IP address back to client
    s.sendto("Detected external address: %s\n" % clientaddr[0], clientaddr)
    #Send received port number
    s.sendto("Detected port: %s\n" % clientaddr[1], clientaddr)
    #Add peer to list of recent connections
    addpeer(clientaddr[0], clientaddr[1], list)
    time.sleep(2)
    #Send list of recent connections (with client's details omitted)
    s.sendto(str(list[:-1]), clientaddr)


##### ACTIVE SECTION OF SCRIPT #####

#Create list for recent connections to send as possible peers
#peerlist = []

#False data for local testing
peerlist = [["2.126.122.29", 8990], ["81.4.56.190", 52708]]

#Set up sockets
s = socketcreate(sys.argv[1], int(sys.argv[2]))
#Main server loop
while True:
    serverloop(s, peerlist)
