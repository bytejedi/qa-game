// 优先级队列具体实现，此处没有做线程安全
package main

import (
	"container/heap"
)

// Item in the PriorityQueue.
//type Item struct {
//	client   *Client
//	Value    *Client
//	Priority int
//	Index    int
//}

// PriorityQueue as implemented by a min heap
// ie. the 0th element is the *lowest* value.
type PriorityQueue []*Client

// New creates a PriorityQueue of the given capacity.
func NewPriorityQueue(capacity int) *PriorityQueue {
	if capacity <= 0 {
		capacity = 1
	}
	pq := make(PriorityQueue, 0, capacity)
	heap.Init(&pq)
	return &pq
}

// Len returns the length of the queue.
func (pq PriorityQueue) Len() int {
	return len(pq)
}

// Less returns true if the item at index i has a lower priority than the item
// at index j.
func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].profile.Grade < pq[j].profile.Grade
}

// Swap the items at index i and j.
func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

// Push a new value to the queue.
func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	c := cap(*pq)
	if n+1 > c {
		npq := make(PriorityQueue, n, c*2)
		copy(npq, *pq)
		*pq = npq
	}
	*pq = (*pq)[0 : n+1]
	item := x.(*Client)
	item.Index = n
	(*pq)[n] = item
}

// Pop an item from the queue.
func (pq *PriorityQueue) Pop() interface{} {
	n := len(*pq)
	c := cap(*pq)
	if n < (c/4) && c > 25 {
		npq := make(PriorityQueue, n, c/2)
		copy(npq, *pq)
		*pq = npq
	}
	item := (*pq)[n-1]
	item.Index = -1 // 安全起见，剔除队列的元素index设为-1
	*pq = (*pq)[0 : n-1]
	return item
}

// Remove an item from the queue
//func (pq *PriorityQueue) Remove(i int) interface{} {
//	if i >= pq.Len() {
//		return errors.New("index out of range")
//	}
//	heap.Remove(pq.items, i)
//	return
//
//}

// PeekAndShift based the max priority.
func (pq *PriorityQueue) PeekAndShift(max int) (*Client, int) {
	if pq.Len() == 0 {
		return nil, 0
	}

	item := (*pq)[0]
	if item.profile.Grade > max {
		return nil, item.profile.Grade - max
	}
	heap.Remove(pq, 0)

	return item, 0
}

// 优先级队列是否存在某个元素
func (pq *PriorityQueue) Exists(item *Client) bool {
	if item.Index >= 0 && len(*pq) > item.Index && (*pq)[item.Index].profile.UserName == item.profile.UserName {
		return true
	}
	return false
}
