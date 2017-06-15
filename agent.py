import os
import time

recvtorrent = open("receivedtorrent.torrent", "r")
torrentdata = recvtorrent.read()

#Find original filename in torrent file metadata
#Check to see if filename length is 1 or 2 digits
#(eg. "name9:..." or "name10:...")
start = (torrentdata.find("name")) + 5
#If char 5 is ":", filename length is under 10
#End index is start + number before ":"
if (torrentdata[start] == ":"):
    start += 1
    end = start + (int(torrentdata[start-2]))
#Else, filename length is double digits
else:
    start += 2
    end = start + (int(torrentdata[(start-3):(start-1)]))

#Remove file extension
filename = torrentdata[start:end].split(".")
filename = filename[0] + ".torrent"

print filename + "\n"

#Create and write torrent files in transmission directory
newtorrent = open("/var/lib/transmission-daemon/downloads/%s" % filename, "w+")
newtorrent.write(torrentdata)

#Close files
newtorrent.close()
recvtorrent.close()

#Add new torrent to transmission daemon
os.system("transmission-remote -n '[USERNAME:PASSWORD]' -a /var/lib/transmission-daemon/downloads/%s" % filename)
time.sleep(1)
os.system("transmission-remote -n '[USERNAME:PASSWORD]' -l")

