// Copyright © 2015 Steve Francia <spf@spf13.com>.
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.
//

package commands

import (
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/isaiah/gc6/mazelib"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type Maze struct {
	rooms      [][]mazelib.Room
	start      mazelib.Coordinate
	end        mazelib.Coordinate
	icarus     mazelib.Coordinate
	StepsTaken int
}

// Tracking the current maze being solved

// WARNING: This approach is not safe for concurrent use
// This server is only intended to have a single client at a time
// We would need a different and more complex approach if we wanted
// concurrent connections than these simple package variables
var currentMaze *Maze
var scores []int

// Defining the daedalus command.
// This will be called as 'laybrinth daedalus'
var daedalusCmd = &cobra.Command{
	Use:     "daedalus",
	Aliases: []string{"deadalus", "server"},
	Short:   "Start the laybrinth creator",
	Long: `Daedalus's job is to create a challenging Labyrinth for his opponent
  Icarus to solve.

  Daedalus runs a server which Icarus clients can connect to to solve laybrinths.`,
	Run: func(cmd *cobra.Command, args []string) {
		RunServer()
	},
}

func init() {
	rand.Seed(time.Now().UTC().UnixNano()) // need to initialize the seed
	gin.SetMode(gin.ReleaseMode)

	RootCmd.AddCommand(daedalusCmd)
}

// Runs the web server
func RunServer() {
	// Adding handling so that even when ctrl+c is pressed we still print
	// out the results prior to exiting.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		printResults()
		os.Exit(1)
	}()

	// Using gin-gonic/gin to handle our routing
	r := gin.Default()
	v1 := r.Group("/")
	{
		v1.GET("/awake", GetStartingPoint)
		v1.GET("/move/:direction", MoveDirection)
		v1.GET("/done", End)
	}

	r.Run(":" + viper.GetString("port"))
}

// Ends a session and prints the results.
// Called by Icarus when he has reached
//   the number of times he wants to solve the laybrinth.
func End(c *gin.Context) {
	printResults()
	os.Exit(1)
}

// initializes a new maze and places Icarus in his awakening location
func GetStartingPoint(c *gin.Context) {
	initializeMaze()
	startRoom, err := currentMaze.Discover(currentMaze.Icarus())
	if err != nil {
		fmt.Println("Icarus is outside of the maze. This shouldn't ever happen")
		fmt.Println(err)
		os.Exit(-1)
	}
	mazelib.PrintMaze(currentMaze)

	c.JSON(http.StatusOK, mazelib.Reply{Survey: startRoom})
}

// The API response to the /move/:direction address
func MoveDirection(c *gin.Context) {
	var err error

	switch c.Param("direction") {
	case "left":
		err = currentMaze.MoveLeft()
	case "right":
		err = currentMaze.MoveRight()
	case "down":
		err = currentMaze.MoveDown()
	case "up":
		err = currentMaze.MoveUp()
	}

	var r mazelib.Reply

	if err != nil {
		r.Error = true
		r.Message = err.Error()
		c.JSON(409, r)
		return
	}

	s, e := currentMaze.LookAround()

	if e != nil {
		if e == mazelib.ErrVictory {
			scores = append(scores, currentMaze.StepsTaken)
			r.Victory = true
			r.Message = fmt.Sprintf("Victory achieved in %d steps \n", currentMaze.StepsTaken)
		} else {
			r.Error = true
			r.Message = err.Error()
		}
	}

	r.Survey = s

	c.JSON(http.StatusOK, r)
}

func initializeMaze() {
	currentMaze = createMaze()
}

// Print to the terminal the average steps to solution for the current session
func printResults() {
	fmt.Printf("Labyrinth solved %d times with an avg of %d steps\n", len(scores), mazelib.AvgScores(scores))
}

// Return a room from the maze
func (m *Maze) GetRoom(x, y int) (*mazelib.Room, error) {
	if x < 0 || y < 0 || x >= m.Width() || y >= m.Height() {
		return &mazelib.Room{}, errors.New("room outside of maze boundaries")
	}

	return &m.rooms[y][x], nil
}

func (m *Maze) Width() int  { return len(m.rooms[0]) }
func (m *Maze) Height() int { return len(m.rooms) }

// Return Icarus's current position
func (m *Maze) Icarus() (x, y int) {
	return m.icarus.X, m.icarus.Y
}

// Set the location where Icarus will awake
func (m *Maze) SetStartPoint(x, y int) error {
	r, err := m.GetRoom(x, y)

	if err != nil {
		return err
	}

	if r.Treasure {
		return errors.New("can't start in the treasure")
	}

	r.Start = true
	m.icarus = mazelib.Coordinate{x, y}
	return nil
}

// Set the location of the treasure for a given maze
func (m *Maze) SetTreasure(x, y int) error {
	r, err := m.GetRoom(x, y)

	if err != nil {
		return err
	}

	if r.Start {
		return errors.New("can't have the treasure at the start")
	}

	r.Treasure = true
	m.end = mazelib.Coordinate{x, y}
	return nil
}

// LookAround Given Icarus's current location, Discover that room
// Will return ErrVictory if Icarus is at the treasure.
func (m *Maze) LookAround() (mazelib.Survey, error) {
	if m.end.X == m.icarus.X && m.end.Y == m.icarus.Y {
		fmt.Printf("Victory achieved in %d steps \n", m.StepsTaken)
		return mazelib.Survey{}, mazelib.ErrVictory
	}

	return m.Discover(m.icarus.X, m.icarus.Y)
}

// Discover Given two points, survey the room.
// Will return error if two points are outside of the maze
// XXX it should check connected rooms, currently the PrintMaze
// function ignores the West and North wall, meh
func (m *Maze) Discover(x, y int) (mazelib.Survey, error) {
	if r, err := m.GetRoom(x, y); err != nil {
		return mazelib.Survey{}, nil
	} else {
		return r.Walls, nil
	}
}

// MoveLeft Moves Icarus's position left one step
// Will not permit moving through walls or out of the maze
func (m *Maze) MoveLeft() error {
	s, e := m.LookAround()
	if e != nil {
		return e
	}
	if s.Left {
		return errors.New("Can't walk through walls")
	}

	x, y := m.Icarus()
	if _, err := m.GetRoom(x-1, y); err != nil {
		return err
	}

	m.icarus = mazelib.Coordinate{x - 1, y}
	m.StepsTaken++
	return nil
}

// Moves Icarus's position right one step
// Will not permit moving through walls or out of the maze
func (m *Maze) MoveRight() error {
	s, e := m.LookAround()
	if e != nil {
		return e
	}
	if s.Right {
		return errors.New("Can't walk through walls")
	}

	x, y := m.Icarus()
	if _, err := m.GetRoom(x+1, y); err != nil {
		return err
	}

	m.icarus = mazelib.Coordinate{x + 1, y}
	m.StepsTaken++
	return nil
}

// Moves Icarus's position up one step
// Will not permit moving through walls or out of the maze
func (m *Maze) MoveUp() error {
	s, e := m.LookAround()
	if e != nil {
		return e
	}
	if s.Top {
		return errors.New("Can't walk through walls")
	}

	x, y := m.Icarus()
	if _, err := m.GetRoom(x, y-1); err != nil {
		return err
	}

	m.icarus = mazelib.Coordinate{x, y - 1}
	m.StepsTaken++
	return nil
}

// Moves Icarus's position down one step
// Will not permit moving through walls or out of the maze
func (m *Maze) MoveDown() error {
	s, e := m.LookAround()
	if e != nil {
		return e
	}
	if s.Bottom {
		return errors.New("Can't walk through walls")
	}

	x, y := m.Icarus()
	if _, err := m.GetRoom(x, y+1); err != nil {
		return err
	}

	m.icarus = mazelib.Coordinate{x, y + 1}
	m.StepsTaken++
	return nil
}

// Creates a maze without any walls
// Good starting point for additive algorithms
func emptyMaze() *Maze {
	z := Maze{}
	ySize := viper.GetInt("height")
	xSize := viper.GetInt("width")

	z.rooms = make([][]mazelib.Room, ySize)
	for y := 0; y < ySize; y++ {
		z.rooms[y] = make([]mazelib.Room, xSize)
		for x := 0; x < xSize; x++ {
			z.rooms[y][x] = mazelib.Room{}
		}
	}

	return &z
}

// Creates a maze with all walls
// Good starting point for subtractive algorithms
func fullMaze() *Maze {
	z := emptyMaze()
	ySize := viper.GetInt("height")
	xSize := viper.GetInt("width")

	for y := 0; y < ySize; y++ {
		for x := 0; x < xSize; x++ {
			z.rooms[y][x].Walls = mazelib.Survey{true, true, true, true}
		}
	}

	return z
}

// TODO: Write your maze creator function here
func createMaze() *Maze {

	// TODO: Fill in the maze:
	// You need to insert a startingPoint for Icarus
	// You need to insert an EndingPoint (treasure) for Icarus
	// You need to Add and Remove walls as needed.
	// Use the mazelib.AddWall & mazelib.RmWall to do this

	return emptyMaze()
}

// MY SOLUTIONS
type Direction int

const (
	E = mazelib.E
	W = mazelib.W
	S = mazelib.S
	N = mazelib.N
)

var (
	DX = map[int]int{
		E: 1,
		W: -1,
		S: 0,
		N: 0,
	}
	DY = map[int]int{
		E: 0,
		W: 0,
		S: 1,
		N: -1,
	}
	OPPOSITE = map[int]int{
		E: W,
		W: E,
		N: S,
		S: N,
	}
	DIRECTIONS = []int{N, W, S, E}
)

// THis is adopted from
// http://weblog.jamisbuck.org/2010/12/27/maze-generation-recursive-backtracking.html
func (m *Maze) carvePassagesFrom(x, y int) {
	for _, i := range rand.Perm(4) {
		d := DIRECTIONS[i]
		nx, ny := x+DX[d], y+DY[d]
		croom, _ := m.GetRoom(x, y)
		room, err := m.GetRoom(nx, ny)
		if err == nil && !room.Visited {
			croom.RmWall(d)
			room.RmWall(OPPOSITE[d])
			room.Visited = true
			m.carvePassagesFrom(nx, ny)
		}
	}
	// TODO: reset visited flag of the rooms
}

// Eller's Algorithm
// http://weblog.jamisbuck.org/2010/12/29/maze-generation-eller-s-algorithm.html
type state struct {
	width   int
	nextSet int
	sets    map[string][]string
	cells   map[string]map[string][]string
}

// Kruskal's Algorithm
// http://weblog.jamisbuck.org/2011/1/3/maze-generation-kruskal-s-algorithm.html

type bitmap map[*mazelib.Room]*tree

func (m *Maze) kruskal() {
	r := rand.New(rand.NewSource(rand.Int63n(99)))
	trees := make(bitmap)
	edges := [][]int{}
	for _, y := range r.Perm(m.Height()) {
		for _, x := range r.Perm(m.Width()) {
			for _, d := range DIRECTIONS[0:2] {
				edges = append(edges, []int{x, y, d})
			}
		}
	}
	for _, i := range r.Perm(len(edges)) {
		edge := edges[i]
		x, y, d := edge[0], edge[1], edge[2]
		nx, ny := x+DX[d], y+DY[d]
		cr, _ := m.GetRoom(x, y)
		if trees[cr] == nil {
			trees[cr] = &tree{}
		}
		nr, _ := m.GetRoom(nx, ny)
		if trees[nr] == nil {
			trees[nr] = &tree{}
		}

		if trees.isConnected(cr, nr) {
			continue
		}
		trees.connect(cr, nr)
		cr.RmWall(d)
		nr.RmWall(OPPOSITE[d])
	}
}

type tree struct {
	parent *tree
}

func (t *tree) root() *tree {
	root := t
	for {
		if root.parent == nil {
			return root
		}
		root = root.parent
	}
}

func (b bitmap) isConnected(cr, nr *mazelib.Room) bool {
	return b[cr].root() == b[nr].root()
}
func (b bitmap) connect(cr, nr *mazelib.Room) {
	b[nr].root().parent = b[cr]
}

// Prim's Algorithm
// http://weblog.jamisbuck.org/2011/1/10/maze-generation-prim-s-algorithm.html
func (m *Maze) neighbors(x, y int) map[*mazelib.Room][]int {
	rooms := make(map[*mazelib.Room][]int)
	// don't change the order, seems like randomness of map iteration is not so
	// random afterall
	if room, err := m.GetRoom(x, y-1); err == nil {
		rooms[room] = []int{x, y - 1}
	}
	if room, err := m.GetRoom(x-1, y); err == nil {
		rooms[room] = []int{x - 1, y}
	}
	if room, err := m.GetRoom(x, y+1); err == nil {
		rooms[room] = []int{x, y + 1}
	}
	if room, err := m.GetRoom(x+1, y); err == nil {
		rooms[room] = []int{x + 1, y}
	}

	return rooms
}

func prim() *Maze {
	m := emptyMaze()
	// frontier
	frontiers := make(map[*mazelib.Room][]int)
	// connected rooms
	in := make(map[*mazelib.Room]bool)
	x := rand.Intn(m.Width())
	y := rand.Intn(m.Height())
	room, _ := m.GetRoom(x, y)
	frontiers[room] = []int{x, y}
	in[room] = true
	for r, loc := range m.neighbors(x, y) {
		frontiers[r] = loc
	}
	// random room
	for len(frontiers) > 0 {
		r := randomRoom(frontiers)
		loc := frontiers[r]
		in[r] = true
		delete(frontiers, r)
		neighbors := m.neighbors(loc[0], loc[1])
		for n, neighbor := range neighbors {
			if in[n] {
				d := direction(loc[0], loc[1], neighbor[0], neighbor[1])
				r.RmWall(d)
				n.RmWall(OPPOSITE[d])
				// only one connected neighbor a time
				break
			}
		}
		// the other not connected neighbors are new frontiers
		for n, neighbor := range neighbors {
			if !in[n] {
				frontiers[n] = neighbor
			}
		}
	}
	return m
}

func randomRoom(f map[*mazelib.Room][]int) *mazelib.Room {
	var rooms []*mazelib.Room
	for r := range f {
		rooms = append(rooms, r)
	}
	return rooms[rand.Intn(len(rooms))]
}

func direction(x, y, nx, ny int) int {
	switch {
	case x < nx:
		return E
	case x > nx:
		return W
	case y < ny:
		return S
	default:
		return N
	}
}

// Recursive division
// http://weblog.jamisbuck.org/2011/1/12/maze-generation-recursive-division-algorithm.html

const (
	HORIZONTAL = 1
	VERTICAL   = 2
)

func recursiveDivision() *Maze {
	m := emptyMaze()
	m.divide(0, 0, m.Width(), m.Height(), chooseOrientation(m.Width(), m.Height()))
	// Add the borders
	w, h := m.Width(), m.Height()
	for x := 0; x < w; x++ {
		r, _ := m.GetRoom(x, h-1)
		r.AddWall(mazelib.S)
	}
	for y := 0; y < h; y++ {
		r, _ := m.GetRoom(w-1, y)
		r.AddWall(mazelib.E)
	}
	return m
}

func chooseOrientation(w, h int) int {
	switch {
	case w < h:
		return HORIZONTAL
	case w > h:
		return VERTICAL
	case rand.Intn(2) == 0:
		return HORIZONTAL
	default:
		return VERTICAL
	}
}

func (m *Maze) divide(x, y, width, height, orientation int) {
	if width < 2 || height < 2 {
		return
	}
	var wx, wy, px, py int
	if orientation == HORIZONTAL {
		if height > 2 {
			wy = y + rand.Intn(height-2)
		} else {
			wy = y
		}
		px = x + rand.Intn(width)
		for wx = x; wx-x < width; wx++ {
			if wx == px {
				continue
			}
			room, _ := m.GetRoom(wx, wy)
			room.AddWall(mazelib.S)
		}
		h := wy - y + 1
		m.divide(x, y, width, h, chooseOrientation(width, h))
		h = y + height - wy - 1
		m.divide(x, wy+1, width, h, chooseOrientation(width, h))
	} else {
		if width > 2 {
			wx = x + rand.Intn(width-2)
		} else {
			wx = x
		}
		py = y + rand.Intn(height)
		for wy = y; wy-y < height; wy++ {
			if wy == py {
				continue
			}
			room, _ := m.GetRoom(wx, wy)
			room.AddWall(mazelib.E)
		}
		w := wx - x + 1
		m.divide(x, y, w, height, chooseOrientation(w, height))
		w = x + width - wx - 1
		m.divide(wx+1, y, w, height, chooseOrientation(w, height))
	}
}
