package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"
)

const (
	_      = iota
	KB int = 1 << (10 * iota)
	MB
	GB
)

//Clock algorithm simulation
type OS struct {
	LogAddrSpace int // given
	LogPageSize  int // given
	LogPageCount int // logAddrSpace/logPageSize

	PhyAddrSpace int // given
	PhyPageCount int // phyAddrSpace/logPageSize

	MaxProcessCount      int //given
	TableRefreshInterval int //given
	SimulationTime       int //given
	MaxExecutionTime     int //given
}

type ProcessQueue []Process
type Process struct {
	PID           int
	ExecutionTime int
	OwnedPages    []VirtualPage
	WorkingSet    []VirtualPage //Most used pages

	Arrow int //points at page
}

//---------------Virtual-------------
var arrow int // Used in Clock algorithm
type VirtualPageTableSwap []VirtualPage
type VirtualPageTable []VirtualPage
type VirtualPage struct {
	P   bool //Presence
	R   bool //Reference(Access)
	M   bool //Modification
	PPN int  //Physical page number
	//Virtual num is PageTable array offset
}

//---------------Physical-------------
type PhysicalPageTable []PhysicalPage
type PhysicalPage struct {
	VirtualPageID int
	//Physical page number is ofset in array
}

func New(logAddrSpace, logPageSize, phyAddrSpace, maxProcessCount, tableRefreshInterval, simulationTime, maxExecutionTime int) *OS {
	return &OS{
		LogAddrSpace: logAddrSpace,
		LogPageSize:  logPageSize,
		LogPageCount: logAddrSpace / logPageSize,

		PhyAddrSpace: phyAddrSpace,
		PhyPageCount: phyAddrSpace / logPageSize,

		MaxProcessCount:      maxProcessCount,
		TableRefreshInterval: tableRefreshInterval,
		SimulationTime:       simulationTime,
		MaxExecutionTime:     maxExecutionTime,
	}
}

//Simulation start
func main() {
	var spt VirtualPageTableSwap
	var vpt VirtualPageTable
	var ppt PhysicalPageTable
	osInfo := New(16*MB, 2*MB, 12*MB, 4, 5, 120, 5)
	for i := 0; i < osInfo.LogPageCount; i++ {
		page := VirtualPage{
			P:   false,
			R:   false,
			M:   false,
			PPN: -1,
		}
		vpt = append(vpt, page)
	}

	for i := 0; i < osInfo.PhyPageCount; i++ {
		page := PhysicalPage{
			VirtualPageID: -1,
		}
		ppt = append(ppt, page)
	}

	MMUMap(&vpt, &ppt)

	var processes ProcessQueue
	var pid int
	for i := 0; i < osInfo.MaxProcessCount; i++ {
		process := Process{
			PID:           pid,
			ExecutionTime: 10, //Make const
		}
		rand.Seed(time.Now().UnixNano())
		max := osInfo.LogPageCount / osInfo.MaxProcessCount
		size := rand.Intn(max) + 1
		process.OwnedPages = vpt[i*max : i*max+size]
		fmt.Println(size, osInfo.LogPageCount, osInfo.MaxProcessCount)
		fmt.Println(process.OwnedPages)
		workingSetSize := rand.Intn(len(process.OwnedPages)) + 1
		process.WorkingSet = process.OwnedPages[:workingSetSize]

		processes = append(processes, process)
		pid++
	}

	//------- tick Round Robin -------
	finished := true
	var selectedProcess Process
	for i := 1; i < osInfo.SimulationTime+1; i++ {
		fmt.Printf("Tick : %d\n", i+1)
		//fmt.Println(processes)
		if finished {
			if len(processes) > 0 {
				selectedProcess = processes.pop()
			} else {
				fmt.Println("All processes are done")
				return
			}
			finished = false
		}
		fmt.Println("SELECTED : ", selectedProcess)

		//-----Clock-----
		//Global policy, pass full table
		choice := rand.Float32()
		if choice < 0.9 {
			MMU(&selectedProcess, &selectedProcess.WorkingSet, &spt, &ppt, &vpt)
			selectedProcess.ExecutionTime--
		} else {
			MMU(&selectedProcess, &selectedProcess.OwnedPages, &spt, &ppt, &vpt)
			selectedProcess.ExecutionTime--
		}
		fmt.Println("Selected lifetime : ", selectedProcess.ExecutionTime)
		//-----Clock-----

		if selectedProcess.ExecutionTime == 0 {
			finished = true
			fmt.Printf("pid : %d  -- finished\n", selectedProcess.PID)
			for i := 0; i < len(selectedProcess.OwnedPages); i++ {
				selectedProcess.OwnedPages[i].P = false
				selectedProcess.OwnedPages[i].R = false
				selectedProcess.OwnedPages[i].M = false
				selectedProcess.OwnedPages[i].PPN = -1
			}

			continue
		}
		if i%osInfo.MaxExecutionTime == 0 {
			processes.put(selectedProcess)
			finished = true
			fmt.Println("PUT : ", processes)
		}
		status(&vpt, &ppt)
	}

}

//Manages page access
func MMU(process *Process, vpt *[]VirtualPage, spt *VirtualPageTableSwap, ppt *PhysicalPageTable, globalVpt *VirtualPageTable) {
	chosenIndex := rand.Intn(len(*vpt))
	fmt.Println("CHOSEN ", chosenIndex)
	fmt.Println("PID ", process.PID)
	if !((*vpt)[chosenIndex].P) {
		fmt.Printf("page fault (pid - %d) : (index - %d)\n", process.PID, chosenIndex)
		victimID := Clock(process, globalVpt)
		(*vpt)[chosenIndex].PPN = (*globalVpt)[victimID].PPN
		(*vpt)[chosenIndex].R = true //--
		(*vpt)[chosenIndex].P = true //--
		*spt = append(*spt, (*globalVpt)[victimID])
		(*globalVpt)[victimID].P = false
		(*globalVpt)[victimID].R = false
		(*globalVpt)[victimID].M = false
		(*globalVpt)[victimID].PPN = -1
	} else {
		chosenVal := rand.Float32()
		if chosenVal > 0.5 {
			(*vpt)[chosenIndex].M = true
		}
		(*vpt)[chosenIndex].R = true
		(*vpt)[chosenIndex].P = true
	}
}

//Init virtual to logical tbles ties
func MMUMap(vpt *VirtualPageTable, ppt *PhysicalPageTable) {
	for i, _ := range *ppt {
		(*ppt)[i].VirtualPageID = i
		(*vpt)[i].PPN = i
		(*vpt)[i].P = true

	}
}

//Global policy one arrow is used
func Clock(process *Process, vpt *VirtualPageTable) int {
	var counter int
	for {
		if !(*vpt)[arrow].P {
			counter++
			fmt.Println("!P")
			arrow++
		} else if (*vpt)[arrow].R {
			fmt.Println("R reset")
			(*vpt)[arrow].R = false
			arrow++
		} else {
			fmt.Println("Chosen Victim : ", arrow)
			return arrow
		}
		fmt.Println("Arrow index : ", arrow)
		if arrow == len(*vpt)-1 {
			arrow = 0
		}
		if counter > len(*vpt) {
			fmt.Println("No avaliable pages left.")
			os.Exit(1)
		}
	}
	return arrow
}

func status(vpt *VirtualPageTable, ppt *PhysicalPageTable) {
	s1, _ := json.MarshalIndent(vpt, "", "\t")
	s2, _ := json.MarshalIndent(ppt, "", "\t")
	fmt.Println("----VPT----", string(s1))
	fmt.Println("----PPT----", string(s2))
}

func (queue *ProcessQueue) pop() Process {
	elem := (*queue)[0]
	*queue = (*queue)[1:len(*queue)]
	return elem
}

func (queue *ProcessQueue) put(pr Process) {
	*queue = append(*queue, pr)
}
