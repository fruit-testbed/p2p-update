import socket
import sys
import time

#Set up socket and return it
def socketcreate(host, port):
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind((host, port))
    return s


#Adds a peer to dictionary of recent clients
#Doesn't return anything
def addpeer(addr, port, peers):
    #Add client address as key (str)
    #Add client port as associated value (int)
    peers[addr] = int(port)
    print peers


def keepalive(addr, port, dictionary, s):
    print "\nMaintaining mapping to %s %d...\n" % (addr, port)
    s.sendto("KeepAliveProxy peerupdate %s" % dictionary, (addr, port))
    #Send up-to-date dictionary of potential peer candidates
    #s.sendto("PeerCandidates update %s" % str(dictionary.items()), (addr, port))


def talkto(addr, port, peers, msg, s):
    #msg format: "TalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address
    #Look for port associated with address in dictionary
    ext_port = peers[msg[1]]
    try:
        s.sendto("TalkRequest %s %d" % (addr, port), (msg[1], ext_port))
        print "sent TalkRequest %s %d to %s %d" % (addr, port, msg[1], ext_port)
    except:
        print "Error sending TalkRequest"
    

def respondto(addr, port, peers, msg, s):
    #msg format: "RespondTo 2.166.240.67 44512"
    msg = msg.split(" ")
    #msg[1] = address
    #Look for port associated with address
    ext_port = " "
    ext_port = peers[msg[1]]
    try:
        s.sendto("TalkResponse %s %d" % (addr, port), (msg[1], ext_port))
        print "sent TalkResponse %s %d to %s %d" % (addr, port, msg[1], ext_port)
    except:
        print "Error sending TalkResponse"


#Main server loop
#Takes a socket as argument 
#Doesn't return anything
def serverloop(s, dictionary):
    #Receive data and address
    msg, clientaddr = s.recvfrom(4096)
    #Keep connection alive between client and server
    if "KeepAliveProxy" in msg:
        keepalive(clientaddr[0], clientaddr[1], dictionary, s)
    #Client requesting to talk to another client
    elif "TalkTo" in msg:
        print "Request received from %s %d: %s" % (clientaddr[0], clientaddr[1], msg)
        talkto(clientaddr[0], clientaddr[1], dictionary, msg, s)
    #Client responding to a TalkTo request from another client
    elif "RespondTo" in msg:
        print "Request received from %s %d: %s" % (clientaddr[0], clientaddr[1], msg)
        respondto(clientaddr[0], clientaddr[1], dictionary, msg, s)
    elif "GetInfo" in msg:
        print "\nReceived message from %s on port %s" % (clientaddr[0], clientaddr[1])
        print "\'%s\'" % msg
        s.sendto("Message received\n", clientaddr)
        #Send received external IP address back to client
        s.sendto("%s" % clientaddr[0], clientaddr)
        #Send received port number
        s.sendto("%s" % clientaddr[1], clientaddr)
        #Add peer to list of recent connections
        addpeer(clientaddr[0], clientaddr[1], dictionary)
        #Send list of recent connections
        s.sendto(str(dictionary.items()), clientaddr)
#        return clientaddr[0], clientaddr[1]
        s.sendto("KeepAliveProxy ...", clientaddr)
    else:
        pass


##### ACTIVE SECTION OF SCRIPT #####

#Create list for recent connections to send as possible peers
#peerlist = []

#False data for local testing
peerlist = [["2.126.122.29", 8990], ["81.4.56.190", 52708]]

#Create dictionary for potential peers
#Entry format - addr: port (str, int)
peercandidates = dict()

#Set up sockets
s = socketcreate(sys.argv[1], int(sys.argv[2]))
#Main server loop
while True:
    serverloop(s, peercandidates)
