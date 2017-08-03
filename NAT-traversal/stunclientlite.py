import os
import sys
import socket
import time
import ast

##### FUNCTION DEFINITIONS #####

def createsocket():
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    return s

def getnatinfo(addr, port, peercandidates, s):
    localaddress = s.getsockname()
    s.sendto("GetInfo: My locally detected address is %s on port %s" % (localaddress[0], localaddress[1]), (addr, port))
    print "Initial recv: %s" % s.recv(4096)
    #Receive public-facing IP address from server
    #(message only (arg[0]), strip address (arg[1]))
    external_ip = s.recvfrom(4096)[0]
    print external_ip
    #Receive external port used to receive UDP traffic from proxy server
    #external_port will be used to communicate with peers later
    external_port = s.recvfrom(4096)[0]
    print external_port
    external_port = external_port.split(": ")
    external_port = external_port[-1]
    time.sleep(1)
    #Receive current list of potential peers in contact with proxy server
    peerdata = s.recvfrom(4096)[0]
    #print "Peerdata: %s" % str(peerdata)
    #Convert received list into dictionary
    peercandidates.update(ast.literal_eval(peerdata))
    #print "Peercandidates: %s" % str(peercandidates)
    return external_ip, external_port


#Keep the addr/port open to receive messages from other clients
def keepaliveproxy(addr, port, msg, peercandidates, s):
    #incoming msg format: "KeepAliveProxy peerupdate [(list-of-peers)]"
    #(ie. (MessageType) (divider) (string-representation-of-dictionary-info))
    try:
        peerdata = msg.split(" peerupdate ")
        #print peerdata[1]
        peercandidates.update(ast.literal_eval(peerdata[1]))
        #print "Peercandidates: %s" % str(peercandidates)
    except:
        pass
    s.sendto("KeepAliveProxy ... ", (addr, port))
    time.sleep(2)
    
    
#Keep the addr/port open to receive messages from other clients
def keepalivepeer(addr, peers, msg, s):
    #incoming msg format: "KeepAlivePeer 2.221.45.10"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" ")
    #msg[1] = address of peer
    #ext_port = port used by peer
    ext_port = peers[msg[1]]
    s.sendto("KeepAlivePeer %s ... " % addr, (msg[1], ext_port))
    print "Sent to %s %d" % (msg[1], ext_port)
    time.sleep(2)    


#Send response to server confirm communication with another client
def sendresponse(addr, port, retransmit, peers, msg, s):
    #incoming msg format: "RespondTo 2.221.45.10"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" ")
    #msg[1] = address of peer
    if retransmit == 0:
        s.sendto("RespondTo %s" % msg[1], (addr, port))
    #If TalkTo message has not been sent, try sending it
    #Necessary for establishing sessions with peers behind restricted NAT
#    if retransmit == 0:
        s.sendto("TalkTo %s" % msg[1], (addr, port))
        #Increment flag to mark retransmission of message
        retransmit = 1
    print "Response sent to %s %s" % (addr, port)
    return retransmit
    

def sessionstart(addr, peers, msg, s):
    #incoming msg format: "SessionStart 2.221.45.10"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" ")
    #msg[1] = address
    #ext_port = port used by peer
    ext_port = peers[msg[1]]
    s.sendto("SessionStart %s" % addr, (msg[1], ext_port))
    print "Custom message sent to %s %s" % (msg[1], ext_port)


#Add a peer to list of current peers in this session
def addsessionpeer(sessionpeers, retransmit, peerlist, msg):
    #msg format: "SessionStart 2.221.45.10"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" ")
    #msg[1] = address
    #ext_port = port used by peer
    ext_port = peerlist[msg[1]]
    #Add peer addr:port (str:int) to dictionary
    sessionpeers[msg[1]] = ext_port

    
def endsession(addr, msg, peers, s):
    #msg format: "EndSession 2.221.45.10"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" ")
    #msg[1] = address
    #ext_port = port used by peer
    ext_port = peers[msg[1]]
    s.sendto("EndSession %s" % addr, (msg[1], ext_port))
    #Remove all peers from session dictionary
    peers.clear()
    print "Ended session with %s %s" % (msg[1], ext_port)


###### ACTIVE SECTION OF SCRIPT #####

#List of received messages
msglist = []

#Dictionary of potential peers
peercandidates = dict()

#Dictionary of peers linked to in current session
sessionpeers = dict()

s = createsocket()
#Arguments: address of proxy server, port, socket
natinfo = getnatinfo(sys.argv[1], int(sys.argv[2]), peercandidates, s)

#True when KeepAlive with proxy is still required
#False when independent session established between peers
proxycontact = True

#True when session has been established between peers
#False when no link confirmed between peers or session has been terminated
sessionlink = False

#ID to align correct responses with received messages
msgid = 0

#Check if message has been retransmitted
#1 if yes, 0 if no
retransmit = 0

#Main loop
while True:
#    print "Retransmit = %d" % retransmit
    msg = s.recvfrom(4096)[0]
    print "msg: %s" % msg
    #KeepAlive request/response cycle with server to keep UDP port open on local NAT
    #if "KeepAliveProxy" in msg:
    if ("KeepAliveProxy" in msg) and (proxycontact):
        keepaliveproxy(sys.argv[1], int(sys.argv[2]), msg, peercandidates, s)
        
    #Catch received KeepAliveProxy messages when proxycontact not enabled
    elif ("KeepAliveProxy" in msg) and (not proxycontact):
        print "Proxy contact disabled - KeepAliveProxy message not sent"
        
    #KeepAlive request/response cycle with peer to keep UDP port open on local NAT
    #elif "KeepAlivePeer" in msg:
    elif ("KeepAlivePeer" in msg) and (sessionlink):
        keepalivepeer(natinfo[0], sessionpeers, msg, s)
        
    #Catch received KeepAliveProxy messages when sessionlink not enabled
    elif ("KeepAlivePeer" in msg) and (not sessionlink):
        print "Peer contact disabled - KeepAlivePeer message not sent"
        
    #TalkRequest sent from another peer through server
    #Sent in response to another peer wanting direct communication with this machine
    elif "TalkRequest" in msg:
        retransmit = sendresponse(sys.argv[1], int(sys.argv[2]), retransmit, peercandidates, msg, s)
        
    #TalkResponse sent from another peer through server
    #Start session with peer independent of proxy server
    elif "TalkResponse" in msg and (not sessionlink):
        #proxycontact = False
        sessionlink = True
        #Add peer to dictionary of peers in this session
        addsessionpeer(sessionpeers, retransmit, peercandidates, msg)
        sessionstart(natinfo[0], peercandidates, msg, s)
    
    #Catch excess TalkResponse messages
    elif "TalkResponse" in msg and (sessionlink):
        pass
        
    #Independent session established with peer, set proxycontact to False
    #Stops KeepAlive cycle with proxy server
    elif "SessionStart" in msg:        
        #proxycontact = False
        sessionlink = True
        #Add peer to dictionary of peers in this session
        addsessionpeer(sessionpeers, retransmit, peercandidates, msg)
        #Send keepalive signal to peer
        keepalivepeer(natinfo[0], sessionpeers, msg, s) 
           
    #End session established with peer, return to keepalive link with proxy server
    elif ("EndSession" in msg) and (sessionlink):
        #Independent session with peer ended, end sessionlink and resume proxycontact
        proxycontact = True
        sessionlink = False
        endsession(natinfo[0], msg, sessionpeers, s)
        #Reset retransmit flag to 0
        retransmit = 0
        #Restart contact with proxy server
        #Exception due to no dictionary in msg will be caught
        keepaliveproxy(sys.argv[1], int(sys.argv[2]), msg, peercandidates, s)
        
    #Catch excess EndSession messages
    elif ("EndSession" in msg) and (not sessionlink):
        pass
    
    #A mysterious message has appeared...
    else:
        print "Unknown message received: %s" % msg
    
    #Increment message counter
    
    
    
