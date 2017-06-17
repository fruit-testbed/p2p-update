import os
import sys
import time

print sys.argv[1]
#Check if supplied argument is a torrent file
#If it is, prompt user to just enter a regular file so torrent file obeys timestamp standards
filedetect = sys.argv[1].split(".")
if filedetect[1] == "torrent":
    #Broadcast specified torrent file as a serf event
    os.system('"`cat %s`"' %sys.argv[1])
#If it's not, create a torrent file first, then trigger Serf event
else:
    os.system('transmission-create -o %s.torrent %s' % (filedetect[0], sys.argv[1]))
    os.system('serf event update "`cat %s.torrent`"' % filedetect[0])

#Allow time for serf event script to resolve
time.sleep(2)

#Retrieve torrent file data sent through Serf
recvtorrent = open("receivedtorrent.torrent", "r")
torrentdata = recvtorrent.read()
print torrentdata

#Find original filename in torrent file metadata
#Check to see if filename length is 1 or 2 digits
#(eg. "name9:..." or "name10:...")
start = (torrentdata.find("name")) + 5
print "start: %d" % start
#If char 5 is ":", filename length is under 10
#End index is start + number before ":"
if (torrentdata[start] == ":"):
    start += 1
    end = start + (int(torrentdata[start-2]))
#Else, filename length is double digits
else:
    start += 2
    end = start + (int(torrentdata[(start-3):(start-1)]))

#Find creation date of torrent file (eg. "13:creation datei1497661746e")
datestart = (torrentdata.find("creation date")) + 14
dateend = datestart + 10
creationdate = torrentdata[datestart:dateend]

#Create file with timestamp as base and .torrent extension
filename = creationdate + ".torrent"

print "Creating %s...\n" % filename

#Create and write torrent files in transmission directory
newtorrent = open("/var/lib/transmission-daemon/downloads/%s" % filename, "w+")
newtorrent.write(torrentdata)

#Check this is the most recent torrent file (ie. the newest update)
#Initial check to prevent outdated updates being downloaded
#This system works for .torrent files being submitted to agent.py, not so well for base files (eg. update.pp)
#Need more sophisticated system to check version of base files being submitted - these torrent files are created by this script and will therefore always register as being the most recent updates available
torrents = os.listdir("/var/lib/transmission-daemon/downloads")
newest = filename
for i in range(len(torrents)):
    #If there is a torrent which is newer, break the loop
    if torrents[i] > filename:
        print "Received update is outdated and will not be downloaded"
        break

#Close files
newtorrent.close()
recvtorrent.close()

#If received update is newest available, add new torrent to transmission daemon
if newest == filename:
    os.system("transmission-remote -n 'USERNAME:PASSWORD' -a /var/lib/transmission-daemon/downloads/%s" % filename)
#Sleep to allow time for added torrent to register in list
time.sleep(5)
os.system("transmission-remote -n 'USERNAME:PASSWORD' -l")
print os.system("ls /var/lib/transmission-daemon/downloads")
