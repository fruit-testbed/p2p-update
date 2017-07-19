import os
import sys
import time
import uuid
import subprocess
import torrentformat


########## FUNCTION DEFINITIONS ###########

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
    sysarg = sys.argv[1]
    return filepath, filewithext, filenoext, fileext, sysarg


#Sending torrent files
def sendtorrent(filepath, filewithext, filenoext, fileext, sysarg):
    print "Warning: download will only commence if source file for submitted torrent is in directory /var/lib/transmission-daemon/downloads/"
    #Sanitise torrent file unicode to prevent corruption when sent over serf
    torrentdata = torrentformat.encodetorrent(fileinfo[4])
    #Attach MD5 hash of torrent file to torrentdata
    #Arguments: string, torrentfile
    torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % fileinfo[2])
    #Broadcast specified torrent file as a serf event
    os.system('serf event update "%s"' % torrentdata)


#Sending directories and extensionless files
def senddirectory(filepath, filewithext, filenoext, fileext, sysarg):
    #If it's a directory, zip, create torrent, generate MD5 signature and send as serf event
    if os.path.isdir(sysarg):
        #Create zip
        os.system('sudo zip -r %s%s.zip %s' % (filepath, filenoext, sysarg))
        #Create torrent of zip file
        os.system('sudo transmission-create -o %s.torrent %s%s.zip' % (filenoext, filepath, filenoext))
        #Sanitise torrent file unicode to prevent corruption when sent over serf
        torrentdata = torrentformat.encodetorrent("%s.torrent" % filenoext)
        #Attach MD5 hash of torrent file to torrentdata
        #Arguments: string, torrentfile
        torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % filenoext)
        #Send torrent data
        os.system('serf event update "%s"' % torrentdata)
        #Cleanup local torrent file
        os.system('sudo rm %s.torrent' % filenoext)
    #If it's an extensionless file, create torrent and send through serf
    else:
        print "Copying %s to %s%s/ ..." % (filewithext, filepath, filenoext)
        #Copy file to transmission folder
        os.system('sudo cp %s %s' % (sysarg, filepath))
        #Create torrent
        print "Creating %s.torrent ..." % filenoext
        os.system('sudo transmission-create -o %s.torrent %s%s' % (filenoext, filepath, filewithext))
        #Sanitise torrent file unicode to prevent corruption when sent over serf
        torrentdata = torrentformat.encodetorrent("%s.torrent" % filenoext)
        #Attach MD5 hash of torrent file to torrentdata
        #Arguments: string, torrentfile
        torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % filenoext)
        #Send torrent data
        os.system('serf event update "%s"' % torrentdata)
        #Cleanup local torrent file
        os.system('sudo rm %s.torrent' % filenoext)


#Sending public keys
def sendpubkey(filepath, filewithext, filenoext, fileext, sysarg):
    print "Public key submitted for torrenting"
    pubkeyid = uuid.uuid4()
    print "Copying %s to %s%s/ ..." % (pubkeyid, filepath, filenoext)
    os.system('sudo cp %s %s%s.pem' % (sysarg, filepath, pubkeyid))
    os.system('sudo transmission-create -o %s.torrent %s%s.pem' % (pubkeyid, filepath, pubkeyid))
    #Sanitise torrent file unicode to prevent corruption when sent over serf
    torrentdata = torrentformat.encodetorrent("%s.torrent" % pubkeyid)
    #Attach MD5 hash of torrent file to torrentdata
    #Arguments: string, torrentfile
    torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % pubkeyid)
    #Send torrent data
    os.system('serf event update "%s"' % torrentdata)
    #Cleanup local torrent file
    os.system('sudo rm %s.torrent' % pubkeyid)


#Sending any other filetype
def sendotherfile(filepath, filewithext, filenoext, fileext, sysarg):
    print "Copying %s to %s%s/ ..." % (filewithext, filepath, filenoext)
    #Copy file to transmission folder
    os.system('sudo cp %s %s' % (sysarg, filepath))
    #Create torrent
    print "Creating %s.torrent ..." % filenoext
    os.system('sudo transmission-create -o %s.torrent %s%s' % (filenoext, filepath, filewithext))
    #Sanitise torrent file unicode to prevent corruption when sent over serf
    torrentdata = torrentformat.encodetorrent("%s.torrent" % filenoext)
    #Attach MD5 hash of torrent file to torrentdata
    #Arguments: string, torrentfile
    torrentdata = torrentformat.appendmd5(torrentdata, "%s.torrent" % filenoext)
    #Send torrent data
    os.system('serf event update "%s"' % torrentdata)
    #Cleanup local torrent file
    os.system('sudo rm %s.torrent' % filenoext)


########## ACTIVE SECTION OF SCRIPT ###########

#Obtain filename, path and extension
fileinfo = getfileinfo(sys.argv[1])
#Set filepath, filewithext, filenoext, fileext, sysarg
fileinfo = setvariables(fileinfo)

#Submitted file was .torrent:
if fileinfo[3] == "torrent":
    sendtorrent(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4])
#Submitted file was directory or extensionless file:
elif fileinfo[3] == "":
    senddirectory(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4])
#Submitted file was a public key  
elif fileinfo[3] == "pem":
    sendpubkey(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4])
#Submitted file was any other type
else:
    sendotherfile(fileinfo[0], fileinfo[1], fileinfo[2], fileinfo[3], fileinfo[4])  


