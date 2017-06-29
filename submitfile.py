import os
import sys
import time

print sys.argv[1]
#Check if supplied argument is a torrent file
#If it is, can just be sent as a serf event
filedetect = sys.argv[1].split(".")
#Isolate filename without directories
filename = sys.argv[1].split("/")
#If directory given ends with "/", filename[-1] will just be blank
#Directory name is actually at [-2]
#If it's a file or just a dir not given with a trailing slash, name is at [-1]
if sys.argv[1][-1] == "/":
    filename = filename[-2]
else:
    filename = filename[-1]
#If filedetect has only one argument (ie. filedetect[0]), directory was submitted
####TODO: handle directories
if (len(filedetect) > 1) and (filedetect[1] == "torrent"):
    print "Warning: download will only commence if source file for submitted torrent is in directory /var/lib/transmission-daemon/downloads/"
    #Broadcast specified torrent file as a serf event
    os.system('serf event update "`cat %s`"' % sys.argv[1])
#If it's not, create a torrent file first, then trigger Serf event
#If it's not, create a directory with file and matching signature (SHA-256)
#Create torrent file of directory then trigger Serf event
else:
    filepath = "/var/lib/transmission-daemon/downloads/"
    filewithext = filename
    filenoext = filedetect[0]
    sysarg = sys.argv[1]

    print "Copying %s to %s%s/ ..." % (filewithext, filepath, filenoext)
    #Create folder of filename without extension
    os.system('sudo mkdir %s%s' % (filepath, filenoext))
    os.system('sudo cp %s %s%s' % (sysarg, filepath, filenoext))
    #Create message digest for file (SHA-256)
    os.system('sudo openssl dgst -sha256 %s%s/%s > %s%s/hash' % (filepath, filenoext, filewithext, filepath, filenoext))
    #Create signature file from hash
    os.system('sudo openssl pkeyutl -sign -inkey private_key.pem -keyform PEM -in %s%s/hash > %s%s/signature' % (filepath, filenoext, filepath, filenoext))
    #Create torrent of directory
    os.system('sudo transmission-create -o %s.torrent %s%s' % (filenoext, filepath, filenoext))
    os.system('sudo serf event update "`cat %s.torrent`"' % filenoext)
