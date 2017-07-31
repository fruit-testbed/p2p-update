import os
import sys
import socket
import time

##### FUNCTION DEFINITIONS #####

def createsocket():
    #UDP socket for IPv4
     s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
     return s

def createlistener(addr, port):
    #UDP socket for IPv4
     s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
     s.bind((addr, port))
     return s

def getnatinfo(addr, port, s):
    localaddress = s.getsockname()
    s.sendto("GetInfo: My locally detected address is %s on port %s" % (localaddress[0], localaddress[1]), (addr, port))
    print "Initial recv: %s" % s.recv(4096)
    #Receive public-facing IP address from server
    #(message only (arg[0]), strip address (arg[1]))
    external_ip = s.recvfrom(4096)[0]
    print external_ip
    #Receive external port
    external_port = s.recvfrom(4096)[0]
    print external_port
    external_port = external_port.split(": ")
    external_port = external_port[-1]
    time.sleep(1)
    peerlist = s.recvfrom(4096)[0]
    print "Peerlist: %s" % str(peerlist)
    return external_ip, external_port


#Keep the addr/port open to receive messages from other clients
def keepaliveproxy(addr, port, s):
    s.sendto("KeepAliveProxy ... ", (addr, port))
    time.sleep(5)
    
#Keep the addr/port open to receive messages from other clients
def keepalivepeer(addr, port, msg, s):
    #msg format: "TalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address of peer
    #int(msg[2]) = port number used by peer to server
    s.sendto("KeepAlivePeer %s %d ... " % (addr, port), (msg[1], int(msg[2])))
    print "Sent to %s %s" % (msg[1], msg[2])
    time.sleep(5)    


#Send response to server confirm communication with another client
def sendresponse(addr, port, msg, s):
    #msg format: "TalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address of peer
    #int(msg[2]) = port number used by peer to server
    s.sendto("RespondTo %s %s" % (msg[1], msg[2]), (addr, port))
    print "Response sent to %s %s" % (addr, msg[2])

#string = string to be sent to peer
def custommsg(string, addr, port, msg, s, peerlist):
    #msg format: "TalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address
    #int(msg[2]) = port number
    #Add [address, port] of peer to peerlist
    peerlist.append([msg[1], msg[2]])
    s.sendto("CustomMsg %s %s %s" % (addr, port, string), (msg[1], int(msg[2])))
    print "Custom message sent to %s %s" % (msg[1], int(msg[2]))

    
#string = string to be sent to peer
def terminatesession(addr, port, msg, s):
    #msg format: "TalkTo 2.221.45.10 50120"
    msg = msg.split(" ")
    #msg[1] = address
    #int(msg[2]) = port number
    s.sendto("TerminateSession %s %s" % (addr, port), (msg[1], int(msg[2])))
    print "Terminated session with %s %s" % (msg[1], msg[2])


###### ACTIVE SECTION OF SCRIPT #####

#List of potential peers
peerlist = []

s = createsocket()
#Arguments: address of proxy server, port, socket
natinfo = getnatinfo(sys.argv[1], int(sys.argv[2]), s)

#True when KeepAlive with proxy is still required
#False when independent session established between peers
proxycontact = True

#True when session has been established between peers
#False when no link confirmed between peers or session has been terminated
sessionlink = False

#Main loop
while True:
    msg = s.recvfrom(4096)[0]
    print "msg: %s" % msg
    #KeepAlive request/response cycle with server to keep UDP port open on local NAT
    #if "KeepAliveProxy" in msg:
    if ("KeepAliveProxy" in msg) and (proxycontact):
        keepaliveproxy(sys.argv[1], int(sys.argv[2]), s)
        
    #Catch incorrectly sent KeepAliveProxy messages
    elif ("KeepAliveProxy" in msg) and (not proxycontact):
        print "Proxy contact disabled - KeepAliveProxy message not sent"
        
    #KeepAlive request/response cycle with peer to keep UDP port open on local NAT
    #elif "KeepAlivePeer" in msg:
    elif ("KeepAlivePeer" in msg) and (sessionlink):
        keepalivepeer(natinfo[0], int(natinfo[1]), msg, s)
        
    #Catch incorrectly sent KeepAliveProxy messages
    elif ("KeepAlivePeer" in msg) and (not sessionlink):
        print "Peer contact disabled - KeepAlivePeer message not sent"
        
    #TalkRequest sent from another peer through server
    #Sent in response to another peer wanting direct communication with this machine
    elif "TalkRequest" in msg:
        sendresponse(sys.argv[1], int(sys.argv[2]), msg, s)
        
    #TalkResponse sent from another peer through server
    #Sent to confirm another peer wanting direct communication with this machine
    elif "TalkResponse" in msg:
        proxycontact = False
        sessionlink = True
        #Send custom message to test traversal
        custommsg(sys.argv[3], natinfo[0], int(natinfo[1]), msg, s, peerlist)
        
    #Independent session established with peer, set proxycontact to False
    #Stops KeepAlive cycle with proxy server
    elif "CustomMsg" in msg:        
        proxycontact = False
        sessionlink = True
        #keepalivepeer(msg, s)
        keepalivepeer(natinfo[0], int(natinfo[1]), msg, s) 
           
    #Terminate session established with peer, return to keepalive link with proxy server
    elif ("TerminateSession" in msg) and (sessionlink):
        #Independent session with peer ended, set proxycontact to True
        #Resumes KeepAlive cycle with proxy server
        proxycontact = True
        sessionlink = False
        terminatesession(natinfo[0], natinfo[1], msg, s)
        keepaliveproxy(sys.argv[1], int(sys.argv[2]), s)
        
    #Catch excess TerminateSession messages
    elif ("TerminateSession" in msg) and (not sessionlink):
        pass
    
    else:
        print "Unknown message received: %s" % msg
    
    
    
    
