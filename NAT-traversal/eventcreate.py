import socket
import os
import sys
import time
import uuid
import subprocess
import torrentformat

##### FUNCTION DEFINITIONS #####

def createsocket():
    #UDP socket for IPv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    return s
    

def sendinput(string, s):
    #Send generated string (i.e event type and payload) to localsocket in stunclientlite.py
    print "Sent message to 127.0.0.1:\n%s" % string
    s.sendto(string, ("127.0.0.1", 5044))
    

#Retrieve the filename and filetype from the filepath given on command line
#Returns filename, filetype
#filename = name of file without path
#Given by array of filepath split by '/'
#(eg. '/home/pi/folder1/bar.py' -> ['', 'home', 'pi', 'folder1', 'bar.py'])
#filetype = ['path-to-file', 'file-extension']
#Given by array of filepath split by '.'
#(eg. '/home/pi/somefile.sh' -> ['home/pi/somefile', '.sh']')
def getfileinfo(givenfile):
    print givenfile
    #Check if supplied argument is a torrent file
    #If it is, can just be sent as a serf event
    filetype = givenfile.split(".")
    #Isolate filename without directories
    filename = givenfile.split("/")
    #If directory given ends with "/", filename[-1] will be blank
    #Directory name is actually at [-2]
    #If it's a file or just a dir not given with a trailing slash, name is at [-1]
    if givenfile[-1] == "/":
        filename = filename[-2]
    else:
        filename = filename[-1]
    return filename, filetype


#Set variables
#Returns filepath, filewithext, filenoext, fileext, sysarg
def setvariables(fileinfo):
    #Downloads folder for transmission-remote operations
    filepath = "/var/lib/transmission-daemon/downloads/"
    #Filename with no filepath
    #eg. 'script.py'
    filewithext = fileinfo[0]
    #Filepath without extension
    #eg. '/home/pi/folder2/script'
    filenoext = fileinfo[1][0]
    #File extension
    #eg. 'py'
    #Will fail if directory was submitted - set to empty string instead
    try:
        fileext = fileinfo[1][1]
    except:
        fileext = ""
    #Full filepath given in command line
    #eg. '/home/pi/folder2/script.py'
    sysarg = sys.argv[2]
    return filepath, filewithext, filenoext, fileext, sysarg


#Sending torrent files
def sendtorrent(filepath, filewithext, filenoext, fileext, sysarg, s):
    print "Warning: download will only commence if source file for submitted torrent is in directory /var/lib/transmission-daemon/downloads/"
    #Sanitise torrent file unicode to prevent corruption when sent over serf
    torrentdata = torrentformat.encodetorrent(fileinfo[4])
    #Attach MD5 hash of torrent file to torrentdata
    #Arguments: string, torrentfile
    torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % fileinfo[2])
    #Send torrent data to client script through socket bound to localhost
    sendinput("SendTorrent %s" % torrentdata, s)


#Sending directories and extensionless files
def senddirectory(filepath, filewithext, filenoext, fileext, sysarg, s):
    #If it's a directory, zip, create torrent, generate MD5 signature and send torrent file data to other peers
    if os.path.isdir(sysarg):
        #Create zip
        os.system('sudo zip -r %s%s.zip %s' % (filepath, filenoext, sysarg))
        #Create torrent of zip file
        os.system('sudo transmission-create -o %s.torrent %s%s.zip' % (filenoext, filepath, filenoext))
        #Sanitise torrent file unicode to prevent corruption when sent over UDP
        torrentdata = torrentformat.encodetorrent("%s.torrent" % filenoext)
        #Attach MD5 hash of torrent file to torrentdata
        #Arguments: string, torrentfile
        torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % filenoext)
        #Send torrent data to client script through socket bound to localhost
        sendinput("SendTorrent " % torrentdata, s)
        #Cleanup local torrent file
        os.system('sudo rm %s.torrent' % filenoext)
    #If it's an extensionless file, create torrent and send through UDP
    else:
        print "Copying %s to %s%s/ ..." % (filewithext, filepath, filenoext)
        #Copy file to transmission folder
        os.system('sudo cp %s %s' % (sysarg, filepath))
        #Create torrent
        print "Creating %s.torrent ..." % filenoext
        os.system('sudo transmission-create -o %s.torrent %s%s' % (filenoext, filepath, filewithext))
        #Sanitise torrent file unicode to prevent corruption when sent over UDP
        torrentdata = torrentformat.encodetorrent("%s.torrent" % filenoext)
        #Attach MD5 hash of torrent file to torrentdata
        #Arguments: string, torrentfile
        torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % filenoext)
        #Send torrent data to client script through socket bound to localhost
        sendinput("SendTorrent %s" % torrentdata, s)
        #Cleanup local torrent file
        os.system('sudo rm %s.torrent' % filenoext)


#Sending public keys
def sendpubkey(filepath, filewithext, filenoext, fileext, sysarg, s):
    print "Public key submitted for torrenting"
    pubkeyid = uuid.uuid4()
    print "Copying %s to %s%s/ ..." % (pubkeyid, filepath, filenoext)
    os.system('sudo cp %s %s%s.pem' % (sysarg, filepath, pubkeyid))
    os.system('sudo transmission-create -o %s.torrent %s%s.pem' % (pubkeyid, filepath, pubkeyid))
    #Sanitise torrent file unicode to prevent corruption when sent over UDP
    torrentdata = torrentformat.encodetorrent("%s.torrent" % pubkeyid)
    #Attach MD5 hash of torrent file to torrentdata
    #Arguments: string, torrentfile
    torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % pubkeyid)
    #Send torrent data to client script through socket bound to localhost
    sendinput("SendTorrent %s" % torrentdata, s)
    #Cleanup local torrent file
    os.system('sudo rm %s.torrent' % pubkeyid)


#Sending any other filetype
def sendotherfile(filepath, filewithext, filenoext, fileext, sysarg, s):
    print "Copying %s to %s%s/ ..." % (filewithext, filepath, filewithext)
    #Copy file to transmission folder
    os.system('sudo cp %s %s' % (sys.argv[2], filepath))
    #Create torrent
    print "Creating %s.torrent ..." % filenoext
    os.system('sudo transmission-create -o %s.torrent %s%s' % (filenoext, filepath, filewithext))
    #Sanitise torrent file unicode to prevent corruption when sent over UDP
    torrentdata = torrentformat.encodetorrent("%s.torrent" % filenoext)
    #Attach MD5 hash of torrent file to torrentdata
    #Arguments: string, torrentfile
    torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % filenoext)
    #Send torrent data to client script through socket bound to localhost
    sendinput("SendTorrent %s" % torrentdata, s)
    #Cleanup local torrent file
    os.system('sudo rm %s.torrent' % filenoext)


########## ACTIVE SECTION OF SCRIPT ###########

#Create socket to send torrent file data
s = createsocket()
s.bind(("127.0.0.1", 11000))

#Type of event being created (ie. first argument given)
#Coverted to lowercase to avoid discrepancies caused by capitalisation
event = (sys.argv[1]).lower()

#SendFile event: send torrent of a file to other peers in the swarm
#Usage: python eventcreate.py sendfile (path-to-some-file)
if event == "sendfile":

    print sys.argv[2]

    #Obtain filename, path and extension
    fileinfo = getfileinfo(sys.argv[2])
    #Set filepath, filewithext, filenoext, fileext, sysarg
    fileinfo = setvariables(fileinfo)

    #Submitted file was .torrent:
    if fileinfo[3] == "torrent":
        sendtorrent(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4], s)
    #Submitted file was directory or extensionless file:
    elif fileinfo[3] == "":
        senddirectory(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4], s)
    #Submitted file was a public key  
    elif fileinfo[3] == "pem":
        sendpubkey(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4], s)
    #Submitted file was any other type
    else:
        sendotherfile(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4], s)  

#EndSession event: leave swarm and notify other peers
#Usage: python eventcreate.py endsession
elif event == "endsession":
    sendinput("EndSession", s)
    
#Exit event: leave swarm, if participating in any, and exit client script
#Usage: python eventcreate.py exit
elif event == "exit":
    sendinput("ExitScript", s)
    
#TalkTo event: establish a session with another peer
#Usage: python eventcreate.py talkto (IP-addr-of-peer)
elif event == "talkto":
    sendinput("TalkTo %s" % sys.argv[2], s)
    
    
    
    
