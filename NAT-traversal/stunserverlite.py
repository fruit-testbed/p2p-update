import socket
import sys
import time

################################
##### FUNCTION DEFINITIONS #####
################################


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


def keepalive(addr, peers, s):
    port = peers[addr]
    print "\nMaintaining mapping to %s %d...\n" % (addr, port)
    #Send up-to-date dictionary of potential peer candidates
    s.sendto("KeepAliveProxy peerupdate %s" % peers, (addr, port))
    #s.sendto("PeerCandidates update %s" % str(dictionary.items()), (addr, port))


def talkto(addr, peers, msg, s):
    #msg format: "TalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address
    #Look for port associated with address in dictionary
    ext_port = peers[msg[1]]
    try:
        s.sendto("TalkRequest %s" % addr, (msg[1], ext_port))
        print "sent TalkRequest %s to %s %d" % (addr, msg[1], ext_port)
    except:
        print "Error sending TalkRequest"
        
def talktorepeat(addr, peers, msg, s):
    #msg format: "RepeatTalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address
    #Look for port associated with address in dictionary
    ext_port = peers[msg[1]]
    try:
        s.sendto("RepeatTalkRequest %s" % addr, (msg[1], ext_port))
        print "sent RepeatTalkRequest %s to %s %d" % (addr, msg[1], ext_port)
    except:
        print "Error sending RepeatTalkRequest"
    

def respondto(addr, peers, msg, s):
    #msg format: "RespondTo 2.166.240.67 44512"
    msg = msg.split(" ")
    #msg[1] = address
    #Look for port associated with address
    ext_port = " "
    ext_port = peers[msg[1]]
    try:
        s.sendto("TalkResponse %s" % addr, (msg[1], ext_port))
        print "sent TalkResponse %s to %s %d" % (addr, msg[1], ext_port)
    except:
        print "Error sending TalkResponse"
        
#Remove peer from dictionary of current peers in contact with proxy server
def removeclient(peers, msg):
    msg = msg.split(" ")
    #Remove peer ceasing contact from dictionary
    del peers[msg[1]]


#Main server loop
#Takes a socket as argument 
#Doesn't return anything
def serverloop(s, dictionary):
    #Receive data and address
    msg, clientaddr = s.recvfrom(4096)
    #Keep connection alive between client and server
    if "KeepAliveProxy" in msg:
        keepalive(clientaddr[0], dictionary, s)
    #Client reattempting to talk to another client
    elif "RepeatTalkTo" in msg:
        print "Repeat Request received from %s %d: %s" % (clientaddr[0], clientaddr[1], msg)
        talktorepeat(clientaddr[0], dictionary, msg, s)
    #Client requesting to talk to another client
    elif "TalkTo" in msg:
        print "Request received from %s %d: %s" % (clientaddr[0], clientaddr[1], msg)
        talkto(clientaddr[0], dictionary, msg, s)
    #Client responding to a TalkTo request from another client
    elif "RespondTo" in msg:
        print "Request received from %s %d: %s" % (clientaddr[0], clientaddr[1], msg)
        respondto(clientaddr[0], dictionary, msg, s)
    #Client sending shutdown notification, remove details from dictionary
    elif "ClientShutdown" in msg:
        print msg
        removeclient(dictionary, msg)
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


####################################
##### ACTIVE SECTION OF SCRIPT #####
####################################


#Create dictionary for potential peers
#Entry format - addr: port (str, int)
peercandidates = dict()

#Set up sockets
s = socketcreate(sys.argv[1], int(sys.argv[2]))

#Main server loop
while True:
    serverloop(s, peercandidates)
