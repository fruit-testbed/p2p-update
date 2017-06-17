import os
import sys
import time
import subprocess

#Detect the most recent Serf event before initiating loop (timestamp and type)
latestevent = subprocess.check_output("tail -2 events.log", shell=True)

#Operate on a constant loop
while True:
    #Check the last 2 lines of the event log to see which Serf event has been received most recently
    eventcheck = subprocess.check_output("tail -2 events.log", shell=True)
    #ie. if a new event has been received
    #eventcheck should only be '' and != latestevent only in the case of dataloss from events.log
    if (eventcheck != latestevent) and (eventcheck != ''):
        latestevent = eventcheck
        #typecheck[0] is a timestamp
        #typecheck[1] is the event type
        typecheck = latestevent.split("\n")
        #routine for Serf 'update' event
        if typecheck[1] == "update":
            print "Update torrent file received"
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

            #Remove base file extension
            #filename = torrentdata[start:end].split(".")
            #Create file with timestamp extension
            filename = creationdate + ".torrent"

            print filename + "\n"

            #Create and write torrent files in transmission directory
            newtorrent = open("/var/lib/transmission-daemon/downloads/%s" % filename, "w+")
            newtorrent.write(torrentdata)

            #Check this is the most recent torrent file (ie. the newest update)
            #Prevents outdated uploads being downloaded
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
