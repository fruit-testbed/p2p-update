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


def keepalive(addr, port, s):
    print "\nMaintaining mapping to %s %d...\n" % (addr, port)
    s.sendto("KeepAliveProxy ... ", (addr, port))


def talkto(addr, port, list, msg, s):
    #msg format: "TalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address
    #Look for port associated with address
    #list should probably be a dictionary instead
    ext_port = " "
    for i in range(len(list)):
        #address found, retrieve port
        if list[i][0] == msg[1]:
            ext_port = list[i][1]
    try:
        s.sendto("TalkRequest %s %d" % (addr, port), (msg[1], int(ext_port)))
    except:
        print "Error sending TalkRequest"
    

def respondto(addr, port, list, msg, s):
    #msg format: "RespondTo 2.166.240.67 44512"
    msg = msg.split(" ")
    #msg[1] = address
    #Look for port associated with address
    ext_port = " "
    for i in range(len(list)):
        #address found, retrieve port
        if list[i][0] == msg[1]:
            ext_port = list[i][1]
    try:
            s.sendto("TalkResponse %s %d" % (addr, port), (msg[1], int(ext_port)))
    except:
        print "Error sending TalkResponse"


#Main server loop
#Takes a socket as argument 
#Doesn't return anything
def serverloop(s, list):
    #Receive data and address
    msg, clientaddr = s.recvfrom(4096)
    #Keep connection alive between client and server
    if "KeepAliveProxy" in msg:
        keepalive(clientaddr[0], clientaddr[1], s)
    #Client requesting to talk to another client
    elif "TalkTo" in msg:
        print "Request received from %s %d: %s" % (clientaddr[0], clientaddr[1], msg)
        talkto(clientaddr[0], clientaddr[1], list, msg, s)
    #Client responding to a TalkTo request from another client
    elif "RespondTo" in msg:
        print "Request received from %s %d: %s" % (clientaddr[0], clientaddr[1], msg)
        respondto(clientaddr[0], clientaddr[1], list, msg, s)
    elif "GetInfo" in msg:
        print "\nReceived message from %s on port %s" % (clientaddr[0], clientaddr[1])
        print "\'%s\'" % msg
        s.sendto("Message received\n", clientaddr)
        #Send received external IP address back to client
        s.sendto("%s" % clientaddr[0], clientaddr)
        #Send received port number
        s.sendto("%s" % clientaddr[1], clientaddr)
        #Add peer to list of recent connections
        addpeer(clientaddr[0], clientaddr[1], list)
        #Send list of recent connections (with client's details omitted)
        s.sendto(str(list[:-1]), clientaddr)
#        return clientaddr[0], clientaddr[1]
        s.sendto("KeepAliveProxy ...", clientaddr)
    else:
        pass


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
