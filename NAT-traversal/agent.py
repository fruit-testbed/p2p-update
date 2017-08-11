import os
import sys
import time
import subprocess
import torrentformat

###########################################
########## FUNCTION DEFINITIONS ###########
###########################################

#Prepare lists on startup
#Return latestevent, torrentcomplete, pubkeylist
def setup():
    #Detect the most recent Serf event before initiating loop (timestamp and type)
    latestevent = subprocess.check_output("tail -2 events.log", shell=True)
    torrentcomplete = []
    pubkeylist = []

    #Populate list of public keys from authorised update nodes
    try:
        pubkeylist = os.listdir("receivedkeys")
    except:
        pass
    print pubkeylist

    try:
       os.system("sudo touch /home/pi/events.log")
    except:
       pass
    return latestevent, torrentcomplete, pubkeylist

#Returns type of last event received through serf and timestamp
def eventcheck(logfile, latestevent):
    #Check the last 2 lines of the event log to see which Serf event has been received most recently
    eventcheck = subprocess.check_output("tail -2 %s" % logfile, shell=True)
    #ie. if a new event has been received
    #eventcheck should only be '' and != latestevent only in the case of dataloss from events.log
    if (eventcheck != latestevent) and (eventcheck != ''):
        latestevent = eventcheck
        #eventcheck[0] is a timestamp
        #eventcheck[1] is the event type
        typecheck = latestevent.split("\n")
        #routine for Serf 'update' event
        if typecheck[1] == "torrent":
            return "torrent", latestevent

#Process the raw torrent data received through serf for an update event
#Returns md5serf, filename, basefilename
#(ie. [md5 hash of torrent file given from serf], [filename of new torrent], [name of file torrent will download])
def processtorrent():
    #Retrieve torrent file data sent through update system
    recvtorrent = open("receivedtorrent.torrent", "r")
    torrentdata = recvtorrent.read()
    #Retrieve original unicode from base64-encoded section received through serf
    md5externalfile = open("md5hash.txt", "r")
    md5external = md5externalfile.read()

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
    basefilename = torrentdata[start:end]
    #Create file with timestamp extension
    filename = creationdate + ".torrent"

    print filename + "\n"

    #Create and write torrent files in transmission directory
    newtorrent = open("/var/lib/transmission-daemon/downloads/%s" % filename, "w+")
    #Remove trailing newline char
    newtorrent.write(torrentdata)
    #Close files
    newtorrent.close()
    recvtorrent.close()
    return md5external, filename, basefilename
    

#Check if most recent torrent received is actually newest update
#Currently based on creation date in torrent file metadata, not trustworthy
#TODO: Replace with blockchain-like method
def versioncheck(filename):
    torrents = os.listdir("/var/lib/transmission-daemon/downloads")
    newest = filename
    for i in range(len(torrents)):
        #Go through torrents to see if any are newer than the most recently received one
        if (".torrent" in torrents[i]) and (torrents[i] > filename):
            newest = torrents[i]
    #Will return true only if no other torrent file is newer
    if newest == filename:
        print "Update is newest available"
        return True
    else:
        print "Received update is outdated and will not be downloaded"
        return False


#Add a torrent to transmission-remote
#Nothing returned
def addtorrent(filename):
    #Try adding torrent file to transmission-remote
    try:
        os.system("transmission-remote -n '[USERNAME]:[PASSWORD]' -a /var/lib/transmission-daemon/downloads/%s" % filename)
    except:
        print "Error adding %s to transmission-remote" % filename
    #Sleep to allow time for added torrent to register in list
    time.sleep(5)
    try:
        os.system("transmission-remote -n '[USERNAME]:[PASSWORD]' -l")
    except:
        print "Error listing transmission downloads"
    print os.system("ls /var/lib/transmission-daemon/downloads")
    

#Monitor active torrents for completed downloads
#Returns basefile if complete download detected, "none" if no new downloads complete
def downloadmonitor():
    try:
         progress = subprocess.check_output("transmission-remote -n '[USERNAME]:[PASSWORD]' -l", shell=True)
    except:
         progress = " "
    #Create list of items which are downloading and their status
    progressitems = progress.split("\n")
    for i in range(len(progressitems)):
        #Ignore lines which are not torrent info
        if "ID" and "Sum" not in progressitems[i]:
        #Torrent ID is chars 0,1,2,3 of string at most
            id = progressitems[i][0:4]
        #If download is newly complete (ie. id not already in torrentcomplete), mark as complete and decide what to do based on file extension
            if ("100%" in progressitems[i]) and (id not in torrentcomplete):
                torrentcomplete.append(id)
                basefilerev = ""
                #Reconstruct basefile name from progressitems[i]
                #Start from reverse of line
                for j in range(len(progressitems[i])-1, -1, -1):
                    if progressitems[i][j] != " ":
                        basefilerev = basefilerev + progressitems[i][j]
                    #If whitespace has been reached, full basefile name has been obtained
                    else:
                        break
                #Reverse string so filename is correct
                basefile = basefilerev[::-1]
                print "Download of %s complete" % basefile
                return basefile
    return "none"


#Action to take when specific filetypes have finished downloading
#Nothing returned
#Potentially recursive if directory was downloaded
def processfile(basefile):
    if ".pp" in basefile:
        print "Applying puppet manifest..."
        os.system("sudo puppet apply /var/lib/transmission-daemon/downloads/%s" % basefile)
    if ".tar" in basefile:
        print "Installing puppet module..."
        os.system("sudo puppet module install /var/lib/transmission-daemon/downloads/%s" % basefile)
    #ie. torrented file was a directory
    if ".zip" in basefile:
        print "Directory received, unzipping..."
        #/home/pi path is hardcoded because ~/ unzips to root/[file]
        #Will try to fix this
        os.system("sudo unzip -o -d /home/pi /var/lib/transmission-daemon/downloads/%s" % basefile)
    if ".pem" in basefile:
        print "Public key received, copying to home directory..."
        try:
            os.system("sudo mkdir /home/pi/receivedkeys")
        except:
            pass
        os.system("sudo cp /var/lib/transmission-daemon/downloads/%s /home/pi/receivedkeys" % basefile)
        #Add new key to list of public keys
        pubkeylist.append(basefile)
    #ie. torrented file was a directory or extensionless file
    if "." not in basefile:
        filepath = "/var/lib/transmission-daemon/downloads/"
        #filelist = list containing update file, hash and signature
        filelist = os.listdir("%s%s" % (filepath, basefile))
        #Determine the full name of the update file
        for i in range(len(filelist)):
            if filelist[i] != "hash" and filelist[i] != "signature":
                updatefile = filelist[i]
                processfile(updatefile)

#Check if the MD5 hash sent over update system for the original torrent file matches the reconstructed local copy
def md5check(filename, md5hash):
    md5local = subprocess.check_output("sudo md5sum /var/lib/transmission-daemon/downloads/%s" % filename, shell=True)
    #Only need MD5 hash, first 32 chars returned by command - ignore filename
    md5local = md5local[:32]
    print md5local
    print md5hash
    if md5local == md5hash:
        print "MD5 hash verified - update is trusted and will be downloaded"
        return True
    else:
        print "MD5 mismatch - potential forgery!"
        return False

###############################################
########## ACTIVE SECTION OF SCRIPT ###########
###############################################

#Return latestevent, torrentcomplete, pubkeylist
output = setup()
latestevent = output[0]
torrentcomplete = output[1]
pubkeylist = output[2]
currenttimestamp = ""
lasttimestamp = ""

#Operate on a constant loop
while True:
    #Detect the most recent event received through serf
    #[event-type, timestamp]
    try:
        event = eventcheck("events.log", latestevent)
        eventtype = event[0]
        currenttimestamp = event[1]
    except:
        pass
    #Only trigger event responses when new event has been received
    if currenttimestamp != lasttimestamp:
        lasttimestamp = currenttimestamp
        #Update file received
        if eventtype == "torrent":
            print "Update torrent file received"
            #Format received torrent data and use it to create new torrent file
            output = processtorrent()
            #MD5 hash of torrent file (filename) given by serf payload
            md5external = output[0]
            #Torrent file created from receivedtorrent.torrent after formatting
            filename = output[1]
            #Name of file which the above torrent file will download
            basefilename = output[2]
            
            #Check that update is newest one available
            newest = versioncheck(filename)
            md5match = md5check(filename, md5external)
            #Add the new torrent to transmission-remote if it's the latest one available and MD5 hash is verified
            if (newest == True) and (md5match == True):
                addtorrent(filename)


    #Download monitoring
    basefile = downloadmonitor()
    if basefile != "none":
        processfile(basefile)
        

           
