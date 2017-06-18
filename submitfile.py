import os
import sys
import time

print sys.argv[1]
#Check if supplied argument is a torrent file
#If it is, prompt user to just enter a regular file so torrent file obeys timestamp standards
filedetect = sys.argv[1].split(".")
#Isolate filename without directories
filename = sys.argv[1].split("/")
filename = filename[-1]
if filedetect[1] == "torrent":
    print "Warning: download will only commence if source file for submitted torrent is in directory /var/lib/transmission-daemon/downloads/"
    #Broadcast specified torrent file as a serf event
    os.system('serf event update "`cat %s`"' % sys.argv[1])
#If it's not, create a torrent file first, then trigger Serf event
else:
    print "Copying %s to /var/transmission-daemon/downloads/ ..." % filename
    os.system('sudo cp %s /var/lib/transmission-daemon/downloads/%s' % (sys.argv[1], filename))
    print "Creating %s.torrent ..." % filedetect[0]
    os.system('transmission-create -o %s.torrent /var/lib/transmission-daemon/downloads/%s' % (filedetect[0], filename))
    os.system('serf event update "`cat %s.torrent`"' % filedetect[0])
#print 'serf event update "$`%s`"' % sys.argv[1]
