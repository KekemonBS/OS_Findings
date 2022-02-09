package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"unsafe"
)

const (
	_        = iota
	KB int64 = 1 << (10 * iota)
	MB
	GB
)

const fileCount = 128
const fileNameSize = 10
const blockSize = 4 * KB
const sectorSize = 1 * KB

//In this program FS = BLOB
//[Superblock-Inodes-DataBlocks]
//-------------------------On Disk---------------------------

type SuperBlock struct {
	FsSize             int64
	BlockTableSize     int64
	FreeBlocksCount    int64
	NextFreeBlockIndex int64

	InodeTableSize     int64
	FreeInodeCount     int64
	NextFreeInodeIndex int64

	Modified bool
}

//Inodes is not fixed, so its array initialize in runtime.

//Inode -> Blocks (file contents)
//Includes metadata and pointers to datablocks
type Inode struct {
	Mode              int8      //for now 0 for file 1 for dir , 2 for symlink, no rwx modes
	Size              int64     //in bytes
	DirrectPointers   [12]int64 //Block index
	IndirrectPointers int64     //Block index with pointers to data blocks (Not Used)
}

//(fileCount*100*1)+(fileCount*8) = 13824 bytes
type Folder struct {
	FileName    [fileCount][fileNameSize]byte //(folder/file) (ASCII) 1-byte
	FileInodeID [fileCount]int64
}

//Representation of file contents
type Block struct {
	Data [blockSize]byte
}

//-------------------------In RAM---------------------------
type OpenFileTable []FileDescriptor

type FileDescriptor struct {
	mode     int8
	offset   int64 //Inode index
	refCount int64
}

//Globals
var SB SuperBlock
var CWD string = "/"
var CurrentInode Inode
var CurrentInodeID int64
var OFT OpenFileTable

//FS with fixed inodes
func mkfs(iq, sz int64) {
	f, _ := os.Create("FS.bin")
	defer f.Close()
	minSize := float64(blockSize) + float64(unsafe.Sizeof(SuperBlock{})) + float64(iq)*float64(unsafe.Sizeof(Inode{}))
	if sz <= int64(minSize) {
		fmt.Println("Invalid size.")
		os.Exit(1)
	}
	f.Truncate(sz)

	sbs := int64(unsafe.Sizeof(SuperBlock{}))
	is := int64(unsafe.Sizeof(Inode{}))
	fbc := int64(math.Floor(float64(sz-sbs-is*iq) / float64(blockSize)))
	fmt.Printf("sbs : %d\n is : %d\nfbc : %d --- %d\n", sbs, is, fbc, fbc*int64(blockSize))
	//Write Superblock on disk
	sb := SuperBlock{sz, fbc, fbc, 0, iq, iq, 0, false}
	SB = sb
	writeSuperBlock(sb)
	//Write empty inodes
	for i := 0; i < int(iq); i++ {
		inode := Inode{Size: int64(i), Mode: 0}
		writeInode(int64(i), inode)
	}

	err := f.Close()
	if err != nil {
		log.Fatal(err)
	}

	//Create root dirrectory
	mkdir("/")
}

func mount() {
	SB = readSuperBlock()
	CurrentInode = readInode(int64(0))
	CurrentInodeID = 0
	fmt.Println(SB)
}

func umount() {
	if SB.Modified {
		SB.Modified = false
		writeSuperBlock(SB)
	}
}

func fstat(id int64) {
	inode := readInode(id)
	dps := fmt.Sprint(inode.DirrectPointers)
	idps := fmt.Sprint(inode.IndirrectPointers)
	fmt.Printf("Mode : %d\nSize : %d\nDirrectPointers : %s\nIndirrectPointers : %s\n",
		inode.Mode,
		inode.Size,
		dps,
		idps)
}

func ls(i Inode) ([fileCount][fileNameSize]byte, [fileCount]int64) {
	validPointers := inodeBids(i)
	currentFolder := readFolder(validPointers)
	var fnms [fileCount][fileNameSize]byte
	var iids [fileCount]int64
	for k, v := range currentFolder.FileName {
		if v[0] == 0 {
			break
		}

		fmt.Printf("%s - %s\n", string(v[:]), fmt.Sprint(currentFolder.FileInodeID[k]))
		fnms[k] = v
		iids[k] = currentFolder.FileInodeID[k]
	}
	return fnms, iids
}

func create(name string) {
	allPointers := CurrentInode.DirrectPointers[:]
	var validPointers []int64
	for k, v := range allPointers {
		if k == 0 || v != 0 {
			validPointers = append(validPointers, v)
		}
	}
	currentFolder := readFolder(validPointers)
	currentFolder = appendToFolder(name, iget(), currentFolder)
	writeFolder(validPointers, currentFolder)
}

func open(name string) {
	_, iid := getInodeByPath(name)
	fileDescriptor := FileDescriptor{0, iid, 1}
	OFT = append(OFT, fileDescriptor)
	fmt.Println(OFT)
}

func close(fd int) {
	OFT = append(OFT[:fd], OFT[fd+1:]...)
}

func read(fd int, buf *[]byte) {
	inode := readInode(OFT[fd].offset)
	bids := inodeBids(inode)

	//Read everything in continuous slice
	for _, v := range bids {
		block := readBlock(v)
		*buf = append(*buf, block.Data[:]...)
	}

}

func write(fd int, buf *[]byte) {
	inode := readInode(OFT[fd].offset)
	bids := inodeBids(inode)
	binBuf := new(bytes.Buffer)
	err := binary.Write(binBuf, binary.BigEndian, *buf)
	if err != nil {
		log.Println(err)
	}
	bbBytes := binBuf.Bytes()
	chunks := chunkSlice(bbBytes, binary.Size(Block{}))
	for k, v := range chunks {
		block := Block{}
		copy(block.Data[:], v)
		writeBlock(bids[k], block)
	}

}

func link(name1, name2 string) {
	allPointers := CurrentInode.DirrectPointers[:]
	_, iid := getInodeByPath(name1)
	//Same as create but with different iid
	var validPointers []int64
	for k, v := range allPointers {
		if k == 0 || v != 0 {
			validPointers = append(validPointers, v)
		}
	}
	fmt.Println(validPointers)
	currentFolder := readFolder(validPointers)
	currentFolder = appendToFolder(name2, iid, currentFolder)
	writeFolder(validPointers, currentFolder)
}

func unlink(name string) {
	//Same as with rmdir but with file check
	validPointers := inodeBids(CurrentInode)
	cwd := readFolder(validPointers)
	for k, v := range cwd.FileName {
		//Check inode if it is file
		inode := readInode(cwd.FileInodeID[k])
		if inode.Mode != 0 {
			continue
		}
		//
		if string(v[:len(name)]) == name {
			fmt.Println("match")

			cwd.FileInodeID[k] = 0
			var emptyName [fileNameSize]byte
			cwd.FileName[k] = emptyName
		}
	}
	writeFolder(validPointers, cwd)

}

func truncate(name string, size int64) {
	inode, iid := getInodeByPath(name)
	fmt.Println("Truncate changing: ", iid)
	var oldSize int64
	for _, v := range inode.DirrectPointers {
		if v != 0 {
			oldSize++
		}
	}
	difference := size - oldSize
	fmt.Println(difference)
	if difference > 0 {
		for i := 0; i < int(math.Abs(float64(difference))); i++ {
			inode.DirrectPointers[int(oldSize)+i] = bget()
		}
	} else {
		for i := 0; i < int(math.Abs(float64(difference))); i++ {
			inode.DirrectPointers[int(oldSize)-i] = 0
		}
	}
	writeInode(iid, inode)
}

func cd(path string) { // Inode {
	//Handle relative path
	if string(path[len(path)-1]) == "/" {
		log.Println("Malformed path, remove last '/'")
		return
	}
	if string(path[0]) == "." {
		path = CWD + path[1:len(path)]
	}
	ci, ciid := getInodeByPath(path)
	if ci.Mode == 1 {
		CurrentInode, CurrentInodeID = ci, ciid
	} else {
		log.Println("Not folder, cpecify different path.")
	}
}

func mkdir(name string) {
	//Change inode
	folder := Folder{}
	fin := iget()

	//Add new inode id to current inode folder
	if name != "/" {
		fmt.Println("not root dir created")

		validPointers := inodeBids(CurrentInode)
		cwd := readFolder(validPointers)
		cwd = appendToFolder(name, fin, cwd)
		writeFolder(validPointers, cwd)

		//Append CurrentInode id as .. , to know how to return
		folder = appendToFolder("..", CurrentInodeID, folder)
	}

	inode := readInode(fin)
	inode.Size = int64(binary.Size(folder))
	inode.Mode = 1
	//In blocks
	folderSize := int(math.Ceil(float64(binary.Size(folder)) / float64(blockSize)))
	fmt.Println("Folder size", folderSize)
	//Should not exceed dirrect pointers
	var bids []int64
	for i := 0; i < folderSize; i++ {
		bid := bget()
		fmt.Println("bget RESULT", bid)
		inode.DirrectPointers[i] = bid
		bids = append(bids, bid)
	}
	writeInode(fin, inode)
	writeFolder(bids, folder)
}

func rmdir(name string) {
	if name == ".." {
		log.Println("Please dont.")
	}
	validPointers := inodeBids(CurrentInode) //Change to traverse path (will return bids of folder down the path)
	cwd := readFolder(validPointers)
	for k, v := range cwd.FileName {
		//Check inode if it is folder
		inode := readInode(cwd.FileInodeID[k])
		if inode.Mode != 1 {
			continue
		}
		//
		if string(v[:len(name)]) == name {
			fmt.Println("match")

			cwd.FileInodeID[k] = 0
			var emptyName [fileNameSize]byte
			cwd.FileName[k] = emptyName
		}
	}
	writeFolder(validPointers, cwd)
}

//------------------------Assistance functions------------------------
//Returns first free inode index
func iget() int64 {
	//Check if there are avaliable space
	if SB.FreeInodeCount == 0 {
		log.Fatal("No avaliable inodes left.")
	}
	//Write what will be returned
	res := SB.NextFreeInodeIndex
	inode := readInode(res)
	//Check if previous run of iget found free inode
	if inode.DirrectPointers[0] != 0 {
		log.Fatal("No avaliable inodes left.")
	}
	//--------Find next candidate--------
	var count int64
	pointer := SB.NextFreeInodeIndex + 1
	for count != SB.InodeTableSize-1 {
		inode := readInode(pointer)
		if inode.DirrectPointers[0] == 0 && inode.Mode == 0 {
			SB.NextFreeInodeIndex = pointer
			break
		}

		if pointer == SB.InodeTableSize-1 {
			pointer = 0
		} else {
			pointer++
		}
		count++
	}
	//--------Find next candidate--------

	//Return previously found inode
	SB.Modified = true
	fmt.Println("iget RESULT", res)
	return res
}

//Returns first free block index
func bget() int64 {
	//Check if there are avaliable space
	if SB.FreeBlocksCount == 0 {
		log.Fatal("No avaliable blocks left.")
	}
	//Write what will be returned
	res := SB.NextFreeBlockIndex
	block := readBlock(res)
	if block.Data[0] != 0 {
		log.Fatal("No avaliable blocks left.")
	}

	//--------Find next candidate--------
	var count int64
	pointer := SB.NextFreeBlockIndex + 1
	for count != SB.BlockTableSize-1 {
		block := readBlock(pointer)
		if block.Data[0] == 0 {
			SB.NextFreeBlockIndex = pointer

			break
		}

		if pointer == SB.BlockTableSize-1 {
			pointer = 0
		} else {
			pointer++
		}
		count++
	}
	//--------Find next candidate--------

	//Return previously found inode
	SB.Modified = true
	fmt.Println("iget RESULT", res)
	return res

}

func readSuperBlock() SuperBlock {
	readRes := make([]byte, binary.Size(SuperBlock{}))
	readH(0, &readRes)
	buffer := bytes.NewBuffer(readRes)
	var res SuperBlock
	err := binary.Read(buffer, binary.BigEndian, &res)
	if err != nil {
		log.Println(err)
	}
	return res
}

func writeSuperBlock(sb SuperBlock) {
	binBuf := new(bytes.Buffer)
	err := binary.Write(binBuf, binary.BigEndian, sb)
	if err != nil {
		log.Println(err)
	}
	bbBytes := binBuf.Bytes()
	writeH(0, &bbBytes)
}

func readInode(in int64) Inode {
	readRes := make([]byte, binary.Size(Inode{}))
	offsetSB := int64(math.Ceil(float64(unsafe.Sizeof(SuperBlock{})) / float64(sectorSize)))
	offsetInode := in * int64(math.Ceil(float64(unsafe.Sizeof(Inode{}))/float64(sectorSize)))
	offset := offsetSB + offsetInode
	readH(offset, &readRes)
	buffer := bytes.NewBuffer(readRes)
	var res Inode
	err := binary.Read(buffer, binary.BigEndian, &res)
	if err != nil {
		log.Println(err)
	}
	return res
}

func writeInode(in int64, i Inode) {
	binBuf := new(bytes.Buffer)
	err := binary.Write(binBuf, binary.BigEndian, i)
	if err != nil {
		log.Println(err)
	}
	bbBytes := binBuf.Bytes()
	offsetSB := int64(math.Ceil(float64(unsafe.Sizeof(SuperBlock{})) / float64(sectorSize)))
	offsetInode := in * int64(math.Ceil(float64(unsafe.Sizeof(Inode{}))/float64(sectorSize)))
	offset := offsetSB + offsetInode
	writeH(offset, &bbBytes)
}

func readBlock(ib int64) Block {
	readRes := make([]byte, binary.Size(Block{}))
	offsetSB := int64(math.Ceil(float64(unsafe.Sizeof(SuperBlock{})) / float64(sectorSize)))
	offsetInode := int64(SB.InodeTableSize) * int64(math.Ceil(float64(unsafe.Sizeof(Inode{}))/float64(sectorSize)))
	offsetBlock := ib * int64(math.Ceil(float64(unsafe.Sizeof(Block{}))/float64(sectorSize)))
	offset := offsetSB + offsetInode + offsetBlock
	fmt.Println("Block read offset", offset)
	readH(offset, &readRes)
	buffer := bytes.NewBuffer(readRes)
	var res Block
	err := binary.Read(buffer, binary.BigEndian, &res)
	if err != nil {
		log.Println(err)
	}
	//fmt.Println(res)
	return res

}

func writeBlock(ib int64, b Block) {
	binBuf := new(bytes.Buffer)
	err := binary.Write(binBuf, binary.BigEndian, b)
	if err != nil {
		log.Println(err)
	}
	bbBytes := binBuf.Bytes()
	offsetSB := int64(math.Ceil(float64(unsafe.Sizeof(SuperBlock{})) / float64(sectorSize)))
	offsetInode := int64(SB.InodeTableSize) * int64(math.Ceil(float64(unsafe.Sizeof(Inode{}))/float64(sectorSize)))
	offsetBlock := ib * int64(math.Ceil(float64(unsafe.Sizeof(Block{}))/float64(sectorSize)))
	offset := offsetSB + offsetInode + offsetBlock
	writeH(offset, &bbBytes)

}

func readFolder(bids []int64) Folder {
	//Read everything in continuous slice
	var contSlice []byte
	for _, v := range bids {
		block := readBlock(v)
		contSlice = append(contSlice, block.Data[:]...)
	}
	//Read needed part into buffer
	readRes := make([]byte, binary.Size(Folder{}))
	for i := 0; i < len(readRes); i++ {
		readRes[i] = contSlice[i]
	}

	buffer := bytes.NewBuffer(readRes)
	fmt.Printf("\n%d --- %d\n", binary.Size(Folder{}), len(readRes))

	var res Folder
	err := binary.Read(buffer, binary.BigEndian, &res)
	if err != nil {
		log.Println(err)
	}
	//fmt.Println(res)
	return res
}

//General way to write data chunked into whatever size
func writeFolder(bids []int64, f Folder) {
	binBuf := new(bytes.Buffer)
	err := binary.Write(binBuf, binary.BigEndian, f)
	if err != nil {
		log.Println(err)
	}
	bbBytes := binBuf.Bytes()
	chunks := chunkSlice(bbBytes, binary.Size(Block{}))
	for k, v := range chunks {
		block := Block{}
		copy(block.Data[:], v)
		writeBlock(bids[k], block)
	}

}

func chunkSlice(slice []byte, chunkSize int) [][]byte {
	var chunks [][]byte
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

func inodeBids(i Inode) []int64 {
	allPointers := i.DirrectPointers[:]
	var validPointers []int64
	for k, v := range allPointers {
		if k == 0 || v != 0 {
			validPointers = append(validPointers, v)
		}
	}
	return validPointers
}

func appendToFolder(name string, iid int64, f Folder) Folder {
	for k, v := range f.FileName {
		if v[0] == 0 {
			copy(f.FileName[k][:], name)
			f.FileInodeID[k] = iid
			break
		}
	}
	return f
}

//Only absolute paths are accepted
func getInodeByPath(path string) (Inode, int64) {
	pathSlice := strings.Split(path, "/")
	fmt.Println(pathSlice)

	//Zero index always empty so will add one to all indexes
	currentTmp := CurrentInode
	var currentTmpID int64
	for i := 1; i < len(pathSlice); i++ {
		fnms, iids := ls(currentTmp)
		for k, v := range fnms {
			if string(v[:len(pathSlice[i])]) == pathSlice[i] {
				fmt.Println("match")

				currentTmp = readInode(iids[k])
				currentTmpID = iids[k]
			}
		}
	}

	return currentTmp, currentTmpID
}

//-------------------------"Hardware" layer -------------------------
//Vauge representation of what could be accessible to user from disk ROM

//Reads binary from "Drive"(file)
//returns buffer with read data
//offset in sectors
func readH(offset int64, buf *[]byte) error {
	f, err := os.Open("./FS.bin")
	if err != nil {
		return err
	}
	defer f.Close()
	f.ReadAt(*buf, offset*sectorSize)
	return nil
}

//Writes to "Drive"
//writes passed buffer with data
//offset in sectors
func writeH(offset int64, buf *[]byte) error {
	f, err := os.OpenFile("./FS.bin", os.O_RDWR, 0644)
	if err != nil {
		log.Println(err)
	}
	defer f.Close()
	_, err = f.WriteAt(*buf, int64(offset*sectorSize))
	if err != nil {
		log.Println(err)
	}
	return nil
}

//-------------------------------------------------------------------

func main() {
	fmt.Println("Representation of FS")

	tmpBuff := make([]byte, 1)
	err := readH(0, &tmpBuff)
	if err != nil {
		log.Println(err)
		log.Println("Run mkfs first")
	}

	for {
		fmt.Println("\nAvaliable commands : mkfs, mount, umount, fstat, ls, create, open, close, read, write, link, unlink, truncate, cd, mkdir, rmdir ")
		fmt.Println("Open file table : ", OFT)
		fmt.Print("Do wish to continue ? [Y/n] : ")

		var ans string
		fmt.Scanf("%s", &ans)
		if ans == "Y" || ans == "y" || ans == "" {
			fmt.Print(">")
			var ansCommand string
			fmt.Scanf("%s", &ansCommand)
			switch ansCommand {
			case "mkfs":
				{
					var iq int64
					fmt.Print("Enter quantity of inodes: ")
					fmt.Scanf("%d", &iq)
					var sz int64
					fmt.Print("Enter size in bytes: ")
					fmt.Scanf("%d", &sz)
					mkfs(iq, sz)
				}
			case "mount":
				{
					mount()
				}
			case "umount":
				{
					umount()
				}
			case "fstat":
				{
					var fd int64
					fmt.Print("Enter file descriptor id: ")
					fmt.Scanf("%d", &fd)
					fstat(fd)
				}
			case "ls":
				{
					ls(CurrentInode)
				}
			case "create":
				{
					var fn string
					fmt.Print("Filename: ")
					fmt.Scanf("%s", &fn)
					create(fn)
				}
			case "open":
				{
					var fn string
					fmt.Print("Filename: ")
					fmt.Scanf("%s", &fn)
					open(fn)
				}
			case "close":
				{
					var fd int
					fmt.Print("Enter file descriptor id: ")
					fmt.Scanf("%d", &fd)
					close(fd)
				}
			case "read":
				{
					var fd int
					fmt.Print("Enter file descriptor id: ")
					fmt.Scanf("%d", &fd)

					var buf []byte
					fmt.Scanf("%d", &buf)

					read(fd, &buf)
					fmt.Println("Read data : ", string(buf))
				}
			case "write":
				{
					var fd int
					fmt.Print("Enter file descriptor id: ")
					fmt.Scanf("%d", &fd)
					var buf string
					fmt.Print("Enter what to write: ")
					fmt.Scanf("%s", &buf)
					convertedString := []byte(buf)
					write(fd, &convertedString)

				}

			case "link":
				{
					var name1 string
					fmt.Print("Enter name of file to be linked to (FILE1): ")
					fmt.Scanf("%s", &name1)
					var name2 string
					fmt.Print("Enter name of link (FILE2): ")
					fmt.Scanf("%s", &name2)
					link(name1, name2)
				}
			case "unlink":
				{
					var linkName string
					fmt.Print("Enter name of link: ")
					fmt.Scanf("%s", &linkName)
					unlink(linkName)
				}
			case "truncate":
				{
					var name string
					fmt.Print("Path: ")
					fmt.Scanf("%s", &name)
					var size int64
					fmt.Print("Enter new size in blocks: ")
					fmt.Scanf("%d", &size)
					truncate(name, size)

				}
			case "cd":
				{
					var path string
					fmt.Print("Enter path: ")
					fmt.Scanf("%s", &path)
					cd(path)

				}
			case "mkdir":
				{
					var fn string
					fmt.Print("Foldername: ")
					fmt.Scanf("%s", &fn)
					mkdir(fn)

				}
			case "rmdir":
				{
					var fn string
					fmt.Print("Foldername: ")
					fmt.Scanf("%s", &fn)
					rmdir(fn)

				}
			}
		} else {
			umount() //To prevent superblock corruption
			os.Exit(0)
		}
	}
}
