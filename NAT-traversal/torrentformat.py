import base64
import io
import sys
import subprocess

#Argument given should be filepath, not raw torrent data
#Converts unicode section of torrent file into base-64 encoding
#Prevents corruption of torrent files when sent over serf
def encodetorrent(torrent):
    torrent = open(torrent, "r")
    string = torrent.read()
    print "%s\n" % string
    #Find the start of the torrent file's unicode
    start = (string.find("pieces")) + 6
    num = ""
    #Read the number of pieces contained in the torrent file
    #(eg. "pieces24:...[unicode here]..."
    #ie. the length of the section which should be encoded
    while string[start].isdigit():
        num = num + string[start]
        start += 1
    #':' is not a digit but also not part of unicode data - skip this char
    start += 1
    #end = end of unicode section in metadata
    end = start + int(num) + 1
    print string[start:end]
    #Encode unicode section in base64
    stringencoded = base64.b64encode(string[start:end])
    #Reconstruct torrent file
    string = string[:start] + stringencoded + string[end:]
#    print stringencoded
    print string
    return string


#Argument given should be a string (ie. raw torrent data)
#Decodes base-64 encoded section of torrent file into original unicode
def decodetorrent(string):
    print string
    #Find the start of the base64 encoded string
    start = (string.find("pieces")) + 6
    num = ""
    #Read the number of pieces contained in the torrent file
    #(eg. "pieces24:...[unicode here]..."
    #ie. the length of the section which should be encoded
    while string[start].isdigit():
        num = num + string[start]
        start += 1
    #':' is not a digit but also not part of unicode data - skip this char
    start += 1
    print "String start = %s" % string[start]
    #end = end of base64 encoded section in metadata
    end = string.find(":private")
    print "String end = %s" % string[end]
    print string[start:end]
    #Decode base64-encoded section to retrieve original unicode
    stringdecoded = base64.b64decode(string[start:end])
    #Reconstruct original torrent file
    print stringdecoded
    string = string[:start] + stringdecoded + string[end:]
    print string
#    print stringdecoded
    return string

#Arguments given:
#string (ie. raw torrent data to be sent over serf)
#torrentfile (torrent file to calculate hash of)
#Appends MD5 hash of torrent file to start of string sent over serf and returns combined string
def appendmd5(string, torrentfile):
    md5hash = subprocess.check_output("sudo md5sum %s" % torrentfile, shell=True)
    string = md5hash[:32] + string
    return string

#Arguments given:
#string (ie. raw torrent data to be sent over serf)
#torrentfile (torrent file to calculate hash of)
#Removes MD5 hash from start of torrent data sent over serf
#Returns [md5hash, string]
def removemd5(string):
#    md5hash = subprocess.check_output("sudo md5sum %s" % torrentfile, shell=True)
    md5hash = string[:32]
    string = string[32:]
    return md5hash, string

