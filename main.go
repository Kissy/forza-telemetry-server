package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"log"
	"math"
	"net"
	"os"
	"strings"
	"time"
)

const hostname = "0.0.0.0"            // Address to listen on (0.0.0.0 = all interfaces)
const port = "43110"                   // UDP Port number to listen on
const service = hostname + ":" + port // Combined hostname+port

// Telemetry struct represents a piece of telemetry as defined in the Forza data format (see the .dat files)
type Telemetry struct {
	position    int
	name        string
	dataType    string
	startOffset int
	endOffset   int
}

func main() {
	// Parse flags
	horizonMode := flag.Bool("z", false, "Enables Forza Horizon 4 support (Will default to Forza Motorsport if unset)")
	debugModePTR := flag.Bool("d", false, "Enables extra debug information if set")
	flag.Parse()

	if *debugModePTR {
		log.Println("Debug mode enabled")
	}

	// Create InfluxDB Client
	client := influxdb2.NewClient("http://localhost:8086", "zPD41f0LWTUzW6A9DxN0AzJqCffKtlKaH5KTeAQD02tK07o84dkTz5qeGoCNdc2uybsdT_9kCqy180coOhADfg==")
	writeAPI := client.WriteAPI("forza", "telemetry")
	defer client.Close()

	// Switch to Horizon format if needed
	var formatFile = "FM7_packetformat.dat" // Path to file containing Forzas data format
	if *horizonMode {
		formatFile = "FH4_packetformat.dat"
		log.Println("Forza Horizon mode selected")
	} else {
		log.Println("Forza Motorsport mode selected")
	}

	// Load lines from packet format file
	lines, err := readLines(formatFile)
	if err != nil {
		log.Fatalf("Error reading format file: %s", err)
	}

	// Process format file into array of Telemetry structs
	startOffset := 0
	endOffset := 0
	dataLength := 0
	var telemArray []Telemetry

	log.Printf("Processing %s...", formatFile)
	for i, line := range lines {
		dataClean := strings.Split(line, ";")          // remove comments after ; from data format file
		dataFormat := strings.Split(dataClean[0], " ") // array containing data type and name
		dataType := dataFormat[0]
		dataName := dataFormat[1]

		switch dataType {
		case "s32": // Signed 32bit int
			dataLength = 4 // Number of bytes
			endOffset = endOffset + dataLength
			startOffset = endOffset - dataLength
			telemItem := Telemetry{i, dataName, dataType, startOffset, endOffset} // Create new Telemetry item / data point
			telemArray = append(telemArray, telemItem)                            // Add Telemetry item to main telemetry array
		case "u32": // Unsigned 32bit int
			dataLength = 4
			endOffset = endOffset + dataLength
			startOffset = endOffset - dataLength
			telemItem := Telemetry{i, dataName, dataType, startOffset, endOffset}
			telemArray = append(telemArray, telemItem)
		case "f32": // Floating point 32bit
			dataLength = 4
			endOffset = endOffset + dataLength
			startOffset = endOffset - dataLength
			telemItem := Telemetry{i, dataName, dataType, startOffset, endOffset}
			telemArray = append(telemArray, telemItem)
		case "u16": // Unsigned 16bit int
			dataLength = 2
			endOffset = endOffset + dataLength
			startOffset = endOffset - dataLength
			telemItem := Telemetry{i, dataName, dataType, startOffset, endOffset}
			telemArray = append(telemArray, telemItem)
		case "u8": // Unsigned 8bit int
			dataLength = 1
			endOffset = endOffset + dataLength
			startOffset = endOffset - dataLength
			telemItem := Telemetry{i, dataName, dataType, startOffset, endOffset}
			telemArray = append(telemArray, telemItem)
		case "s8": // Signed 8bit int
			dataLength = 1
			endOffset = endOffset + dataLength
			startOffset = endOffset - dataLength
			telemItem := Telemetry{i, dataName, dataType, startOffset, endOffset}
			telemArray = append(telemArray, telemItem)
		case "hzn": // Forza Horizon 4 unknown values (12 bytes of.. something)
			dataLength = 12
			endOffset = endOffset + dataLength
			startOffset = endOffset - dataLength
			telemItem := Telemetry{i, dataName, dataType, startOffset, endOffset}
			telemArray = append(telemArray, telemItem)
		default:
			log.Fatalf("Error: Unknown data type in %s \n", formatFile)
		}
		//Debug format file processing:
		if *debugModePTR {
			log.Printf("Processed %s line %d: %s (%s),  Byte offset: %d:%d \n", formatFile, i, dataName, dataType, startOffset, endOffset)
		}
	}

	if *debugModePTR { // Print completed telemetry array
		log.Printf("Logging entire telemArray: \n%v", telemArray)
	}

	log.Printf("Proccessed %d Telemetry types OK!", len(telemArray))

	// Setup UDP listener
	udpAddr, err := net.ResolveUDPAddr("udp4", service)
	if err != nil {
		log.Fatal(err)
	}

	listener, err := net.ListenUDP("udp", udpAddr)
	check(err)
	defer listener.Close() // close after main ends - probably not really needed

	// log.Printf("Forza data out server listening on %s, waiting for Forza data...\n", service)
	log.Printf("Forza data out server listening on %s:%s, waiting for Forza data...\n", GetOutboundIP(), port)

	for { // main loop
		readForzaData(listener, writeAPI, telemArray)
	}
}

// readForzaData processes recieved UDP packets
func readForzaData(conn *net.UDPConn, writeAPI api.WriteAPI, telemArray []Telemetry) {
	buffer := make([]byte, 1500)

	n, addr, err := conn.ReadFromUDP(buffer)
	if err != nil {
		log.Fatal("Error reading UDP data:", err, addr)
	}

	if isFlagPassed("d") == true { // Print extra connection info if debugMode set
		log.Println("UDP client connected:", addr)
		// fmt.Printf("Raw Data from UDP client:\n%s", string(buffer[:n])) // Debug: Dump entire received buffer
	}

	currentEngineRpm := float32(0)

	p := influxdb2.NewPointWithMeasurement("stat")

	// Use Telemetry array to map raw data against Forza's data format
	for i, T := range telemArray {
		data := buffer[:n][T.startOffset:T.endOffset] // Process received data in chunks based on byte offsets

		if isFlagPassed("d") == true { // if debugMode, print received data in each chunk
			log.Printf("Data chunk %d: %v (%s) (%s)", i, data, T.name, T.dataType)
		}

		if T.name == "TimestampMS" {
			timestamp := binary.LittleEndian.Uint32(data)
			p = p.SetTime(time.Unix(0, int64(timestamp) * int64(time.Millisecond)))
		} else if T.name == "CurrentEngineRpm" {
			currentEngineRpm = Float32frombytes(data)
		}

		switch T.dataType { // each data type needs to be converted / displayed differently
		case "s32":
		case "u32":
			// fmt.Println("Name:", T.name, "Type:", T.dataType, "value:", binary.LittleEndian.Uint32(data))
			p = p.AddField(T.name, binary.LittleEndian.Uint32(data))
		case "f32":
			// fmt.Println("Name:", T.name, "Type:", T.dataType, "value:", (dataFloated * 1))
			p = p.AddField(T.name, Float32frombytes(data))
		case "u16":
			// fmt.Println("Name:", T.name, "Type:", T.dataType, "value:", binary.LittleEndian.Uint16(data))
			p = p.AddField(T.name, binary.LittleEndian.Uint16(data))
		case "u8":
			p = p.AddField(T.name, data[0])
		case "s8":
			p = p.AddField(T.name, int8(data[0]))
		}
	}

	// Dont print / log / do anything if RPM is zero
	// This happens if the game is paused or you rewind
	// There is a bug with FH4 where it will continue to send data when in certain menus
	if currentEngineRpm == 0 {
		return
	}

	writeAPI.WritePoint(p.SetTime(time.Now()))
}

func init() {
	log.SetFlags(log.Lmicroseconds)
	log.Println("Started Forza Data Tools")
}

// Helper functions

// Quick error check helper
func check(e error) {
	if e != nil {
		log.Fatalln(e)
	}
}

// Check if flag was passed
func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// Float32frombytes converts bytes into a float32
func Float32frombytes(bytes []byte) float32 {
	bits := binary.LittleEndian.Uint32(bytes)
	float := math.Float32frombits(bits)
	return float
}

// readLines reads a whole file into memory and returns a slice of its lines
func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// GetOutboundIP finds preferred outbound ip of this machine
func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "1.2.3.4:4321") // Destination does not need to exist, using this to see which is the primary network interface
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}
