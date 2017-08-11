import os
import sys
import socket
import time
import ast
import io
import torrentformat

################################
##### FUNCTION DEFINITIONS #####
################################

def createsocket():
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    return s

#Get external-facing address and port info from stunserverlite
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
    try:
        ext_port = peers[msg[1]]
        s.sendto("KeepAlivePeer %s ... " % addr, (msg[1], ext_port))
        print "Sent to %s %d" % (msg[1], ext_port)
        time.sleep(2)    
    except:
        pass

#Send response to server confirm communication with another client
def sendresponse(addr, port, retransmit, peers, msg, s):
    #incoming msg format: "RespondTo 2.221.45.10"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" ")
    #msg[1] = address of peer
    #Maybe set retransmit < 2 instead?
    #Two restricted NATs would need to send this message at least twice each
    if retransmit < 2:
        s.sendto("RespondTo %s" % msg[1], (addr, port))
    #If TalkTo message has not been sent, try sending it
    #Necessary for establishing sessions with peers behind restricted NAT
#    if retransmit == 0:
        s.sendto("TalkTo %s" % msg[1], (addr, port))
        #Increment flag to mark retransmission of message
        retransmit += 1
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
    print "SessionStart sent to %s %s" % (msg[1], ext_port)


#Add a peer to dictionary of current peers in this session
def addsessionpeer(sessionpeers, peerlist, msg):
    #msg format: "SessionStart 2.221.45.10"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" ")
    #msg[1] = address
    #ext_port = port used by peer
    ext_port = peerlist[msg[1]]
    #Add peer addr:port (str:int) to dictionary
    sessionpeers[msg[1]] = ext_port
    
#Remove peer from dictionary of current peers in this session
def removesessionpeer(sessionpeers, msg):
    msg = msg.split(" ")
    #Remove peer leaving session from dictionary
    del sessionpeers[msg[1]]


#Send message to other peers alerting them that this machine is leaving the session
def endsession(addr, peers, s):
    #Send message to other peers in current session
    peerlist = peers.items()
    for i in range(len(peerlist)):
        peeraddr = peerlist[i][0]
        port = peerlist[i][1]
        s.sendto("PeerLeave %s" % addr, (peeraddr, port))
    #Clear sessionpeers dictionary
    peers.clear()


#Tell proxy server to start establishing peer-to-peer session with another client    
def talkto(string, addr, port, retransmit, s):
    #ie. if peer this machine is attempting to contact has never responded, resend TalkTo message to proxt
    if retransmit < 2:
        print "Send %s to %s %d" % (string, addr, port)
        s.sendto(string, (addr, port))
        

#Establishing contact has already failed for some reason, try to request again
#Needed to reset retransmit values in retransmitcount dictionary
def talktorepeat(string, addr, port, retransmit, s):
    #ie. if peer this machine is attempting to contact has never responded, resend TalkTo message to proxt
    if retransmit < 2:
        print "Send Repeat%s to %s %d" % (string, addr, port)
        s.sendto("Repeat%s" % string, (addr, port))
        

#Keep the addr/port open to receive messages from other clients
def sendTorrentFile(addr, peers, msg, s):
    #incoming localmsg format: "SendTorrentFile (MD5-hash-and-torrent-file-data)"
    #(ie. (MessageType) (peer-IP-address))
    msg = msg.split(" split ")
    #msg[1] = address of peer
    #ext_port = port used by peer
    print "Torrentdata: %s" % msg[1]
    peerlist = peers.items()
    print peerlist
    #Send torrentdata to all peers in current session
    for i in range(len(peerlist)):
        try:
            peer_addr = peerlist[i][0]
            peer_port = peerlist[i][1]
            #Outgoing msg format: "SendTorrentFile (own-external-IP-addr) (MD5-hash-and-torrent-file-data)"
            s.sendto("SendTorrentFile %s split %s" % (addr, msg[1]), (peer_addr, peer_port))
            print "SendTorrentFile sent to %s %d" % (peer_addr, peer_port)    
        except:
            pass


#Torrent file received from peer, process MD5 hash and metadata
#Update ~/events.log to notify agent.py script a new update script has been received            
def processtorrent(msg):
    #Incoming msg format: "SendTorrentFile (IP-addr-of-peer) (MD5-hash-and-torrent-file-data)"
    msg = msg.split(" split ")
    print "Processing torrent..."
    home = os.environ['HOME']
    #Retrieve MD5 hash and torrent data from payload
    md5hash, torrentdata = torrentformat.removemd5(msg[1])
    #Decode torrentdata from base64
    hashfile = open("%s/md5hash.txt" % home, "w+")
    torrentfile = open("%s/receivedtorrent.torrent" % home, "w+")
    eventfile = open("%s/events.log" % home, "w+")
    #Write MD5 hash and raw torrent file metadata to files in home directory
    print "Writing MD5 hash to '%s/md5hash.txt'...." % home
    hashfile.write(md5hash)
    hashfile.close()
    print "Writing torrent data to '%s/receivedtorrent.torrent'...." % home
    print "Torrentdata = %s" % torrentdata
    torrentdata = torrentformat.decodetorrent(torrentdata)
    torrentfile.write(torrentdata)
    torrentfile.close()
    #Write timestamp and event type to ~/events.log to alert agent.py of new torrent file
    print "Writing to events log..."
    timestamp = time.time()
    eventfile.write("%f\ntorrent" % timestamp)
    eventfile.close()
    print "Received torrent file processed successfully."
    
    
def sharepeers(peers, msg, s):
    msg = msg.split(" ")
    peer_port = peers[msg[1]]
    peerlist = peers.items()
    s.sendto("SharePeers split %s" % str(peerlist), (msg[1], peer_port))
    print "SharePeers sent to %s %d" % (msg[1], peer_port)
   



#####################################
###### ACTIVE SECTION OF SCRIPT #####
#####################################


#Dictionary of peers and retransmit counts (key:value = peer-addr:retransmit-count)
#TalkTo messages should be send at least twice to ensure peers behind restricted NAT can be contacted
#ie. when the value associated with a key (peer) = 2, TalkTo messages will stop being sent via the proxy server to that peer
retransmitcount = dict()

#Dictionary of potential peers
peercandidates = dict()

#Dictionary of peers linked to in current session
sessionpeers = dict()

#Set up sockets
#Socket to receive messages from servers and other clients
s = createsocket()
#Socket to receive messages from localhost, port 10000
localsocket = createsocket()
#Set socket as non-blocking to avoid script hanging when no data to read
localsocket.setblocking(0)
#Arbitrary port chosen, no significance to 5044
#Can be anything as long as it matches destination port used in eventcreate.py script and isn't the same as the localhost socket bound in that script
localsocket.bind(("127.0.0.1", 5044))

#Obtain IP address of NAT and port used between NAT and proxy server
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

#Check how many times message has been retransmitted
retransmit = 0


#Main loop
while True:

    #Check for incoming messages from external sources (ie. non-localhost)
    
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
        
    #Catch received KeepAlivePeer messages when sessionlink not enabled
    elif ("KeepAlivePeer" in msg) and (not sessionlink):
        print "Peer contact disabled - KeepAlivePeer message not sent"
    
    #RepeatTalkRequest sent from another peer through server
    #Sent when TalkRequest has already failed to establish contact at least once
    elif "RepeatTalkRequest" in msg:
        #Add client in TalkRequest message to retransmitcount if not in already
        client = msg.split(" ")[1]
        #if client in retransmitcount:
        retransmitcount[client] = 0
        print "Client %s retransmitcount reset to 0" % client
        retransmitcount[client] = sendresponse(sys.argv[1], int(sys.argv[2]), retransmitcount[client], peercandidates, msg, s)
        print "retransmit count for %s: %d" % (client, retransmitcount[client])
    
    #TalkRequest sent from another peer through server
    #RespondTo sent in response to enable direct peer-to-peer communication with this machine
    elif "TalkRequest" in msg:
        #Add client in TalkRequest message to retransmitcount if not in already
        client = msg.split(" ")[1]
        if client not in retransmitcount:
            retransmitcount[client] = 0
            print "Client %s added to retransmitcount" % client
        retransmitcount[client] = sendresponse(sys.argv[1], int(sys.argv[2]), retransmitcount[client], peercandidates, msg, s)
        print "retransmit count for %s: %d" % (client, retransmitcount[client])
        
    #TalkResponse sent from another peer through server
    #Start session with peer
    #elif "TalkResponse" in msg and (not sessionlink):
    elif "TalkResponse" in msg:
        sessionlink = True
        #Add peer to dictionary of peers in this session
        addsessionpeer(sessionpeers, peercandidates, msg)
        sessionstart(natinfo[0], peercandidates, msg, s)
    
    #Catch excess TalkResponse messages
    #elif "TalkResponse" in msg and (sessionlink):
        #pass
        
    #Independent session established with peer, set proxycontact to False
    #Stops KeepAlive cycle with proxy server
    elif "SessionStart" in msg:        
        #proxycontact = False
        sessionlink = True
        #Add peer to dictionary of peers in this session
        addsessionpeer(sessionpeers, peercandidates, msg)
        #ie. if there are any peers other than the one just added, share with the client which just joined the session
        if len(sessionpeers) > 1:
            sharepeers(sessionpeers, msg, s)
        #Send keepalive signal to peer
        keepalivepeer(natinfo[0], sessionpeers, msg, s) 
           
    #Peer left current session, remove from sessionpeers dictionary and retransmitcount
    elif ("PeerLeave" in msg) and (sessionlink):
        removesessionpeer(sessionpeers, msg)
        client = msg.split(" ")[1]
        try:
            del retransmitcount[client]
        except:
            pass
        #Exception due to no dictionary in msg will be caught in keepaliveproxy call
        #keepaliveproxy(sys.argv[1], int(sys.argv[2]), msg, peercandidates, s)
 
    #Received a torrent file from a peer
    elif "SendTorrentFile" in msg:
        print "Torrent file received"
        #Separate MD5 hash from torrent data and write components to separate files
        processtorrent(msg)
        
    #Received shared list of peers in current session
    elif "SharePeers" in msg:
        #incoming msg is just a list of peer addresses and ports
        print "Shared list of peers received"
        try:
            msg = msg.split(" split ")
            #Convert string representation of list into actual list
            newpeers = ast.literal_eval(msg[1])
            for i in range(len(newpeers)):
                #Only add a new peer to sessionpeers if it's not in the dictionary already
                #Don't add own address and port details
                if (newpeers[i][0] not in sessionpeers) and (newpeers[i] != natinfo[0]):
                    client = newpeers[i][0]
                    client_port = newpeers[i][1]
                    sessionpeers[client] = client_port
                    #Attempt to establish session with new peers
                    if client not in retransmitcount:
                        retransmitcount[client] = 0
                        print "Client %s added to retransmitcount in initial TalkTo call" % client
                        talkto("TalkTo %s" % client, sys.argv[1], int(sys.argv[2]), retransmitcount[client], s)
                    #If client is in dictionary and talkto command is being used again, communication has already failed somehow
                    #Reset client value to 0 and try again
                    else:
                        retransmitcount[client] = 0
                        print "Client %s retransmitcount reset to 0" % client
                        talktorepeat("TalkTo %s" % client, sys.argv[1], int(sys.argv[2]), retransmitcount[client], s)
        #Exception thrown when string cannot be literally evaluated as a list                               
        except:
            print "Error converting string of shared peers into list"

    #A mysterious message has appeared...
    else:
        print "Unknown message received: %s" % msg
        
    #If there are no peers in the current session, mark sessionlink as False
    if len(sessionpeers) == 0:
        sessionlink = False
    
    # Check for messages from localhost (ie. events to broadcast to proxy server or swarm)
    
    try:
        #Retrieve info from socket bound to localhost
        localmsg = localsocket.recvfrom(4096)[0]
        print localmsg
        
        #SendTorrent event: send torrent file to peers in current session
        if "SendTorrent" in localmsg:
            print "Current sessionpeers: %s" % str(sessionpeers)
            sendTorrentFile(natinfo[0], sessionpeers, localmsg, s)
        
        #EndSession event: exit swarm and notify peers
        elif "EndSession" in localmsg:
            #Independent session with peer ended, end sessionlink and resume proxycontact
            proxycontact = True
            sessionlink = False
            #retransmission = 0
            endsession(natinfo[0], sessionpeers, s)
                
        #TalkTo event: send message containing peer addr to proxy server
        elif "TalkTo" in localmsg:
            client = localmsg.split(" ")[1]
            if client not in retransmitcount:
                retransmitcount[client] = 0
                print "Client %s added to retransmitcount in initial TalkTo call" % client
                talkto(localmsg, sys.argv[1], int(sys.argv[2]), retransmitcount[client], s)
            #If client is in dictionary and talkto command is being used again, communication has already failed somehow
            #Reset client value to 0 and try again
            else:
                retransmitcount[client] = 0
                print "Client %s retransmitcount reset to 0" % client
                talktorepeat(localmsg, sys.argv[1], int(sys.argv[2]), retransmitcount[client], s)
        
        #ExitScript event: call endsession if in contact with peers, then exit script
        #Similar to EndSession event, but removes self from proxy server's list of peers as well
        elif "ExitScript" in localmsg:
            #If peer-to-peer session is active, announce leaving to peers
            if sessionlink:
                #Independent session with peer ended, end sessionlink and resume proxycontact
                proxycontact = True
                sessionlink = False
                endsession(natinfo[0], sessionpeers, s)
                #Send notification of shutdown to proxy server
                s.sendto("ClientShutdown %s" % natinfo[0], (sys.argv[1], int(sys.argv[2])))
            #Exit the program
            print "Exiting client script ..."
            break
            #
            sys.exit()
            
        else:
            print "Unknown localmsg: %s" % localmsg
    #No localmsg found, skip to next iteration of loop
    except:
        pass
