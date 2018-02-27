package main

import (
  "os"
  "bufio"
  "strings"

  "github.com/pkg/errors"
)

func PiSerial() (string, error) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", errors.New("Cannot open /proc/cpuinfo")
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 10 && line[0:7] == "Serial\t" {
			return strings.TrimLeft(strings.Split(line, " ")[1], "0"), nil
		}
	}
	if err := scanner.Err(); err != nil {
		errors.Wrap(err, "Failed to read serial number")
	}
	return "", errors.New("Cannot find serial number from /proc/cpuinfo")
}

func PiPassword() (string, error) {
  // TODO: implement this
  return "123", nil
}
