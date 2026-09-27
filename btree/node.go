package btree

import (
	"context"
	"fmt"
	"sort"

	goccyjson "github.com/goccy/go-json"
	"github.com/sharedcode/joltrin/v5"
)

// MetaDataType specifies metadata fields such as ID and Version.
type MetaDataType interface {
	// GetID returns the object's ID.
	GetID() sop.UUID
	// GetVersion returns the object's version.
	GetVersion() int32
	// SetVersion applies a version to the object.
	SetVersion(v int32)
}

// Item contains a key/value pair and a version number.
type Item[TK Ordered, TV any] struct {
	// (Internal) ID is the Item's sop.UUID. ID is needed for two reasons:
	// 1. so the B-tree can identify or differentiate items with duplicated keys.
	// 2. used as the value data ID if the item's value is persisted in another
	//    data segment, separate from the node segment (IsValueDataInNodeSegment=false).
	ID sop.UUID `json:"ID"`
	// Key is the key part in the key/value pair.
	Key TK `json:"Key"`
	// Value is nil when data is persisted in the separate data segment (with ValueID set to a valid sop.UUID);
	// otherwise it points to the actual data and is persisted in the B-tree node segment together with the key.
	Value *TV `json:"Value,omitempty"`
	// Version is used for conflict resolution among in-flight transactions.
	Version int32 `json:"Version"`
	// ValueNeedsFetch tells the B-tree whether the value data needs fetching.
	// Applicable only when IsValueDataInNodeSegment is false.
	ValueNeedsFetch bool `json:"ValueNeedsFetch,omitempty"`
	// valueWasFetched is for internal use only; it indicates whether the value was just read from the backend.
	valueWasFetched bool
}

func newItem[TK Ordered, TV any](key TK, value TV) *Item[TK, TV] {
	return &Item[TK, TV]{
		Key:   key,
		Value: &value,
		ID:    sop.NewUUID(),
	}
}

// Node contains a B-Tree node's data.
type Node[TK Ordered, TV any] struct {
	ID       sop.UUID `json:"ID"`
	ParentID sop.UUID `json:"ParentID"`
	// Slots is an array where the Items get stored.
	Slots []Item[TK, TV] `json:"Slots"`
	// Count of items in this node.
	Count int `json:"Count"`
	// Version of this node.
	Version int32 `json:"Version"`
	// ChildrenIDs holds the IDs of this node's children.
	ChildrenIDs []sop.UUID `json:"ChildrenIDs,omitempty"`
	indexOfNode int
}

func (n *Node[TK, TV]) MarshalJSON() ([]byte, error) {
	type nodeData struct {
		ID          sop.UUID       `json:"ID"`
		ParentID    sop.UUID       `json:"ParentID"`
		Slots       []Item[TK, TV] `json:"Slots"`
		Count       int            `json:"Count"`
		Version     int32          `json:"Version"`
		ChildrenIDs []sop.UUID     `json:"ChildrenIDs,omitempty"`
	}
	return goccyjson.Marshal(nodeData{
		ID:          n.ID,
		ParentID:    n.ParentID,
		Slots:       n.Slots[:n.Count],
		Count:       n.Count,
		Version:     n.Version,
		ChildrenIDs: n.ChildrenIDs,
	})
}

func (n *Node[TK, TV]) UnmarshalJSON(data []byte) error {
	type nodeData struct {
		ID          sop.UUID       `json:"ID"`
		ParentID    sop.UUID       `json:"ParentID"`
		Slots       []Item[TK, TV] `json:"Slots"`
		Count       int            `json:"Count"`
		Version     int32          `json:"Version"`
		ChildrenIDs []sop.UUID     `json:"ChildrenIDs,omitempty"`
	}
	var nd nodeData

	// Reuse the pre-allocated slice capacities if this node was created via newNode(slotLength).
	// This prevents encoding/json from allocating new backing arrays!
	if cap(n.Slots) > 0 {
		nd.Slots = n.Slots[:0]
	}
	if cap(n.ChildrenIDs) > 0 {
		nd.ChildrenIDs = n.ChildrenIDs[:0]
	}

	if err := goccyjson.Unmarshal(data, &nd); err != nil {
		return err
	}

	n.ID = nd.ID
	n.ParentID = nd.ParentID
	n.Count = nd.Count
	n.Version = nd.Version

	// Expand the slice lengths up to their capacity so elements can be shifted during inserts.
	// Since we handed encoding/json the pre-allocated array, nd.Slots and nd.ChildrenIDs
	// share the same exact backing array as n.Slots and n.ChildrenIDs.
	if nd.Slots != nil {
		n.Slots = nd.Slots[:cap(nd.Slots)]
	} else {
		n.Slots = nil
	}

	if len(nd.ChildrenIDs) > 0 {
		n.ChildrenIDs = nd.ChildrenIDs[:cap(nd.ChildrenIDs)]
	} else {
		n.ChildrenIDs = nil
	}

	return nil
}

// Default Slot Length is 5000.
const DefaultSlotLength = 5000

// GetID returns the node's UUID.
func (n *Node[TK, TV]) GetID() sop.UUID {
	return n.ID
}

// GetVersion returns the node's version.
func (n *Node[TK, TV]) GetVersion() int32 {
	return n.Version
}

// SetVersion updates the node's version to v.
func (n *Node[TK, TV]) SetVersion(v int32) {
	n.Version = v
}

// CopyTo copies this node into an existing destination node, reusing any pre-allocated
// backing slices on the destination when possible.
func (n *Node[TK, TV]) CopyTo(dst any) bool {
	if n == nil || dst == nil {
		return false
	}
	target, ok := dst.(*Node[TK, TV])
	if !ok || target == nil {
		return false
	}

	target.ID = n.ID
	target.ParentID = n.ParentID
	target.Count = n.Count
	target.Version = n.Version
	target.indexOfNode = n.indexOfNode

	if len(n.Slots) == 0 {
		target.Slots = nil
	} else if cap(target.Slots) >= len(n.Slots) {
		target.Slots = target.Slots[:len(n.Slots)]
	} else {
		target.Slots = make([]Item[TK, TV], len(n.Slots))
	}
	copy(target.Slots, n.Slots)

	if len(n.ChildrenIDs) == 0 {
		target.ChildrenIDs = nil
	} else if cap(target.ChildrenIDs) >= len(n.ChildrenIDs) {
		target.ChildrenIDs = target.ChildrenIDs[:len(n.ChildrenIDs)]
	} else {
		target.ChildrenIDs = make([]sop.UUID, len(n.ChildrenIDs))
	}
	copy(target.ChildrenIDs, n.ChildrenIDs)
	return true
}

// Clone returns a deep copy of the node, preserving the current slot capacity while
// copying the contents of the slots and child IDs.
func (n *Node[TK, TV]) Clone() *Node[TK, TV] {
	if n == nil {
		return nil
	}
	clone := &Node[TK, TV]{}
	if !n.CopyTo(clone) {
		return nil
	}
	return clone
}

// CloneMetaData returns a metadata-safe clone of the node for cache use.
func (n *Node[TK, TV]) CloneMetaData() MetaDataType {
	return n.Clone()
}

// newNode creates a new node.
func newNode[TK Ordered, TV any](slotCount int) *Node[TK, TV] {
	return &Node[TK, TV]{
		Slots:       make([]Item[TK, TV], slotCount),
		indexOfNode: -1,
	}
}

// add traverses the B-tree to find the leaf node where the item should be inserted
// according to sort order. The actual insertion on the target node is handled by addOnLeaf.
func (node *Node[TK, TV]) add(ctx context.Context, btree *Btree[TK, TV], item *Item[TK, TV]) (bool, error) {
	var currentNode = node
	var index int
	for {
		var itemExists bool
		index, itemExists = currentNode.getIndexToInsertTo(btree, item)
		// itemExists will be true if and only if btree.IsUnique() is true, thus,
		// will prevent insert of duplicated key item.
		if itemExists {
			// set the Current item pointer to the duplicate item.
			btree.setCurrentItemID(currentNode.ID, index)
			return false, nil
		}
		if currentNode.hasChildren() {
			ok, err := currentNode.addItemOnNodeWithNilChild(btree, item, index)
			if err != nil || ok {
				return ok, err
			}
			// if not an outermost node let next lower level node do the 'Add'.
			currentNode, err = currentNode.getChild(ctx, btree, index)
			if err != nil || currentNode == nil {
				return false, err
			}
		} else {
			break
		}
	}
	if btree.isUnique() && currentNode.Count > 0 {
		var currItemIndex = index
		if index > 0 && index >= currentNode.Count {
			currItemIndex--
		}
		if btree.compare(currentNode.Slots[currItemIndex].Key, item.Key) == 0 {
			// set the Current item pointer to the discovered existing item.
			btree.setCurrentItemID(currentNode.ID, currItemIndex)
			return false, nil
		}
	}
	if err := currentNode.addOnLeaf(ctx, btree, item, index); err != nil {
		return false, err
	}
	return true, nil
}

// addOnLeaf inserts the item on the outermost (leaf) node. At this point, the correct
// node to add the item to has been reached after traversing inner nodes of the B-tree.
func (node *Node[TK, TV]) addOnLeaf(ctx context.Context, btree *Btree[TK, TV], item *Item[TK, TV], index int) error {
	// If node is not yet full, insert and shift items to the right.
	if node.Count < btree.getSlotLength() {
		// Insert the item at the target position and shift items to the right.
		node.insertSlotItem(item, index)
		// Save this node.
		btree.saveNode(node)
		return nil
	}

	// Node is full, distribute or break up the node (use temp slots in the process).
	copy(btree.tempSlots, node.Slots)

	// Index now contains the correct array element number to insert the item into.
	// Shift items to the right and assign the item to the vacated slot.
	copy(btree.tempSlots[index+1:], btree.tempSlots[index:])
	// Set the item to the newly vacated slot.
	btree.tempSlots[index] = *item

	var isVacantSlotInLeft, isVacantSlotInRight bool
	var err error

	slotsHalf := btree.getSlotLength() >> 1
	var isUnBalanced bool
	if !node.isRootNode() {
		isVacantSlotInLeft, err = node.isThereVacantSlotInLeft(ctx, btree, &isUnBalanced)
		if err != nil {
			return err
		}
		isVacantSlotInRight, err = node.isThereVacantSlotInRight(ctx, btree, &isUnBalanced)
		if err != nil {
			return err
		}

		if isVacantSlotInLeft || isVacantSlotInRight {
			// Distribute the overflowed item to either the left or right sibling.
			// Copy temp buffer contents to the actual slots.
			var b int16 = 1
			if isVacantSlotInLeft {
				b = 0
			}
			copy(node.Slots, btree.tempSlots[b:])
			// Save this node.
			btree.saveNode(node)

			btree.distributeAction.sourceNode = node
			if isVacantSlotInLeft {
				btree.distributeAction.item = btree.tempSlots[btree.getSlotLength()]
				clear(btree.tempSlots)

				// Vacancy on the left: request a distribution action from the B-tree.
				// Logic: shift the leftmost node's item to the parent and move the parent's
				// item to the left sibling (recursively).
				btree.distributeAction.distributeToLeft = true
				return nil
			}
			btree.distributeAction.item = btree.tempSlots[0]
			clear(btree.tempSlots)
			// Vacancy on the right: move the rightmost node item into the vacant slot on the right.
			btree.distributeAction.distributeToLeft = false
			return nil
		}

		if isUnBalanced {
			// If this branch is unbalanced, break the full node to create new slots.
			// Description:
			// - copy the left half of the slots
			// - copy the right half of the slots
			// - zero out the current slot
			// - copy the middle slot
			// - allocate memory for children nodes
			// - assign the new children nodes

			// Initialize should throw an exception if in error.
			rightNode := newNode[TK, TV](btree.getSlotLength())
			rightNode.newID(node.ID)
			leftNode := newNode[TK, TV](btree.getSlotLength())
			leftNode.newID(node.ID)
			copyArrayElements(leftNode.Slots, btree.tempSlots, slotsHalf)
			leftNode.Count = slotsHalf
			copyArrayElements(rightNode.Slots, btree.tempSlots[slotsHalf+1:], slotsHalf)

			rightNode.Count = slotsHalf
			clear(node.Slots)
			node.Slots[0] = btree.tempSlots[slotsHalf]

			// Save this Node, Left & Right Nodes.
			btree.saveNode(leftNode)
			btree.saveNode(rightNode)
			node.ChildrenIDs = make([]sop.UUID, btree.getSlotLength()+1)
			node.ChildrenIDs[0] = leftNode.ID
			node.ChildrenIDs[1] = rightNode.ID
			btree.saveNode(node)

			clear(btree.tempSlots)
			return nil
		}
		// All slots are occupied in this and sibling nodes.

		// Prepare this node and the right sibling, then promote the temporary parent node.
		rightNode := newNode[TK, TV](btree.getSlotLength())
		rightNode.newID(node.ParentID)
		// Zero out the node slots in preparation to make it the left sibling.
		clear(node.Slots)

		// Copy the left half of the slots to left sibling(node).
		copyArrayElements(node.Slots, btree.tempSlots, slotsHalf)
		node.Count = slotsHalf
		// Copy the right half of the slots to right sibling.
		copyArrayElements(rightNode.Slots, btree.tempSlots[slotsHalf+1:], slotsHalf)
		rightNode.Count = slotsHalf

		// Copy the middle slot to temp parent slot.
		btree.tempParent = btree.tempSlots[slotsHalf]

		// Assign the new children nodes.
		btree.tempParentChildren[0] = node.ID
		btree.tempParentChildren[1] = rightNode.ID

		p, err := btree.getNode(ctx, node.ParentID)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("can't get parent (ID='%v') of this node", node.ParentID)
		}

		//  Save this and Right Node.
		btree.saveNode(node)
		btree.saveNode(rightNode)

		btree.promoteAction.targetNode = p
		btree.promoteAction.slotIndex = p.getIndexOfChild(node)
		return nil
	}

	// Break this node to create available slots.
	// Description:
	// - copy the left half of the temp slots
	// - copy the right half of the temp slots
	// - zero out the current slot
	// - copy the middle of the temp slot to the 1st element of the current slot
	// - allocate memory for children nodes
	// - assign the new children nodes
	rightNode := newNode[TK, TV](btree.getSlotLength())
	rightNode.newID(node.ID)
	leftNode := newNode[TK, TV](btree.getSlotLength())
	leftNode.newID(node.ID)

	copyArrayElements(leftNode.Slots, btree.tempSlots, slotsHalf)
	leftNode.Count = slotsHalf
	copyArrayElements(rightNode.Slots, btree.tempSlots[slotsHalf+1:], slotsHalf)
	rightNode.Count = slotsHalf
	clear(node.Slots)
	node.Slots[0] = btree.tempSlots[slotsHalf]

	node.Count = 1

	// Save Left and Right Nodes.
	btree.saveNode(leftNode)
	btree.saveNode(rightNode)

	node.ChildrenIDs = make([]sop.UUID, btree.getSlotLength()+1)
	node.ChildrenIDs[0] = leftNode.ID
	node.ChildrenIDs[1] = rightNode.ID

	// Save this Node.
	btree.saveNode(node)
	clear(btree.tempSlots)

	return nil
}

// find walks the tree to locate the key and positions the B-tree cursor.
// If firstItemWithKey is true and duplicates exist, it selects the first match;
// otherwise it selects the exact match or the nearest neighbor when not found
// (to support range scans).
func (node *Node[TK, TV]) find(ctx context.Context, btree *Btree[TK, TV], key TK, firstItemWithKey bool) (bool, error) {
	n := node
	foundItemIndex := 0
	foundNodeID := sop.NilUUID
	var err error
	index := 0
	for n != nil {
		index = 0
		if n.Count > 0 {
			index = sort.Search(n.Count, func(index int) bool {
				return btree.compare(n.Slots[index].Key, key) >= 0
			})
			// If key is found in node n.
			if index < n.Count && btree.compare(n.Slots[index].Key, key) == 0 {
				// Make the found node & item index the "current item" of btree.
				foundNodeID = n.ID
				foundItemIndex = index
				if !firstItemWithKey {
					break
				}
			}
		}
		// Check children if there are.
		if n.hasChildren() {
			// Short circuit if child is nil as there is no more duplicate on left side.
			if n.ChildrenIDs[index] == sop.NilUUID {
				break
			}
			n, err = n.getChild(ctx, btree, index)
			if err != nil {
				return false, err
			}
			continue
		}
		// Short circuit loop if there are no more children.
		break
	}
	if !foundNodeID.IsNil() {
		btree.setCurrentItemID(foundNodeID, foundItemIndex)
		return true, nil
	}
	// This must be the outermost node
	// This block will make this item the current one to give chance to the Btree
	// caller the chance to check the items having the nearest key to the one it is interested at.
	if index == n.Count {
		// make sure i points to valid item
		index--
	}
	if index >= 0 && index < n.Count {
		btree.setCurrentItemID(n.ID, index)
	} else {
		index--
		// Update Current Item of this Node and nearest to the Key in sought Slot index
		btree.setCurrentItemID(n.ID, index)
		// Make the next item the current item. This has the effect of positioning making the next greater item the current item.
		_, err = n.moveToNext(ctx, btree)
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

func (node *Node[TK, TV]) findInDescendingOrder(ctx context.Context, btree *Btree[TK, TV], key TK) (bool, error) {
	n := node
	foundItemIndex := 0
	foundNodeID := sop.NilUUID
	var err error
	index := 0
	for n != nil {
		index = 0
		if n.Count > 0 {
			index = sort.Search(n.Count, func(index int) bool {
				return btree.compare(n.Slots[index].Key, key) > 0
			})
			if index > 0 {
				i := index - 1
				if btree.compare(n.Slots[i].Key, key) == 0 {
					foundNodeID = n.ID
					foundItemIndex = i
				}
			}
		}
		// Check children if there are.
		if n.hasChildren() {
			if n.ChildrenIDs[index] == sop.NilUUID {
				break
			}
			n, err = n.getChild(ctx, btree, index)
			if err != nil {
				return false, err
			}
			continue
		}
		// Short circuit loop if there are no more children.
		break
	}
	if !foundNodeID.IsNil() {
		btree.setCurrentItemID(foundNodeID, foundItemIndex)
		return true, nil
	}
	// This must be the outermost node
	// This block will make this item the current one to give chance to the Btree
	// caller the chance to check the items having the nearest key to the one it is interested at.
	if index == n.Count {
		// make sure i points to valid item
		index--
	}
	if index >= 0 && index < n.Count {
		btree.setCurrentItemID(n.ID, index)
	} else {
		index--
		// Update Current Item of this Node and nearest to the Key in sought Slot index
		btree.setCurrentItemID(n.ID, index)
		// Make the next item the current item. This has the effect of positioning making the next greater item the current item.
		_, err = n.moveToNext(ctx, btree)
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

// moveToFirst positions the cursor at the smallest key in the tree by
// traversing the left-most branch until a leaf is reached.
func (node *Node[TK, TV]) moveToFirst(ctx context.Context, btree *Btree[TK, TV]) (bool, error) {
	n := node
	var prev *Node[TK, TV]
	var err error
	for n.ChildrenIDs != nil {
		prev = n
		cid := n.ChildrenIDs[0]
		// If nil Child, then we've reached the 1st item's node, stop the walk.
		if cid == sop.NilUUID {
			break
		}
		n, err = btree.getNode(ctx, cid)
		if err != nil {
			return false, err
		}
		if n == nil {
			break
		}
	}
	if n != nil {
		prev = n
	}
	btree.setCurrentItemID(prev.ID, 0)
	return true, nil
}

// moveToLast positions the cursor at the largest key in the tree by
// traversing the right-most branch until a leaf is reached.
func (node *Node[TK, TV]) moveToLast(ctx context.Context, btree *Btree[TK, TV]) (bool, error) {
	n := node
	var err error
	for n.ChildrenIDs != nil {
		cid := n.ChildrenIDs[n.Count]
		// If nil Child, then we've reached the last item's node, stop the walk.
		if cid == sop.NilUUID {
			break
		}
		n, err = btree.getNode(ctx, cid)
		if n == nil || err != nil {
			return false, err
		}
	}
	btree.setCurrentItemID(n.ID, n.Count-1)
	return n.ID != sop.NilUUID, nil
}

// moveToNext advances the cursor to the next in-order item.
// When the current node has children, it descends into the right child;
// otherwise it climbs up to the first ancestor where the current node was a left child.
func (node *Node[TK, TV]) moveToNext(ctx context.Context, btree *Btree[TK, TV]) (bool, error) {
	n := node
	slotIndex := btree.currentItemRef.getNodeItemIndex()
	slotIndex++
	goRightDown := n.hasChildren()
	var err error
	if goRightDown {
		for {
			if n == nil {
				btree.setCurrentItemID(sop.NilUUID, 0)
				return false, nil
			}
			if n.hasChildren() {
				if ok, err := n.goRightUpItemOnNodeWithNilChild(ctx, btree, slotIndex); ok || err != nil {
					return ok, err
				}
				n, err = n.getChild(ctx, btree, slotIndex)
				if err != nil {
					return false, err
				}
				slotIndex = 0
			} else {
				btree.setCurrentItemID(n.ID, 0)
				return true, nil
			}
		}
	}
	for {
		if n == nil {
			btree.setCurrentItemID(sop.NilUUID, 0)
			return false, nil
		}
		// Check if SlotIndex is within the maximum slot items and if it is, will index an occupied slot.
		if slotIndex < n.Count {
			btree.setCurrentItemID(n.ID, slotIndex)
			return true, nil
		}
		// Check if this is the root node. (Root nodes don't have a parent.)
		if n.isRootNode() {
			// Root node: set the current item to nil (end of B-tree reached).
			btree.setCurrentItemID(sop.NilUUID, 0)
			return false, nil
		}
		p, err := n.getParent(ctx, btree)
		if err != nil {
			return false, err
		}
		slotIndex = p.getIndexOfChild(n)
		n = p
	}
}

// moveToPrevious moves the cursor to the previous in-order item.
// When the current node has children, it descends into the left neighbor subtree;
// otherwise it climbs up to the first ancestor where the current node was a right child.
func (node *Node[TK, TV]) moveToPrevious(ctx context.Context, btree *Btree[TK, TV]) (bool, error) {
	n := node
	slotIndex := btree.currentItemRef.getNodeItemIndex()
	goLeftDown := n.hasChildren()
	var err error
	if goLeftDown {
		for {
			if n.hasChildren() {
				if ok, err := n.goLeftUpItemOnNodeWithNilChild(ctx, btree, slotIndex); ok || err != nil {
					return ok, err
				}
				n, err = n.getChild(ctx, btree, slotIndex)
				if err != nil {
					return false, err
				}
				if n == nil {
					// Set the current item to nil; end of B-tree reached.
					btree.setCurrentItemID(sop.NilUUID, 0)
					return false, nil
				}
				slotIndex = n.Count
			} else {
				// 'SlotIndex -1' since we are now using SlotIndex as index to pSlots.
				btree.setCurrentItemID(n.ID, slotIndex-1)
				return true, nil
			}
		}
	}
	slotIndex--
	for {
		// Check if SlotIndex is within the maximum slot items and if it is, will index an occupied slot.
		if slotIndex >= 0 {
			btree.setCurrentItemID(n.ID, slotIndex)
			return true, nil
		}
		if n.isRootNode() {
			// Set the current item to nil; end of B-tree reached.
			btree.setCurrentItemID(sop.NilUUID, 0)
			return false, nil
		}
		p, err := n.getParent(ctx, btree)
		if err != nil {
			return false, err
		}
		slotIndex = p.getIndexOfChild(n) - 1
		n = p
	}
}

// fixVacatedSlot removes the current item and restores B-tree invariants.
// It compacts slots for leaf deletions, handles special root cases, and
// unlinks empty nodes when necessary.
func (node *Node[TK, TV]) fixVacatedSlot(ctx context.Context, btree *Btree[TK, TV]) error {
	position := btree.currentItemRef.getNodeItemIndex()
	deletedItem := node.Slots[position]
	if err := btree.storeInterface.ItemActionTracker.Remove(ctx, &deletedItem); err != nil {
		return err
	}
	// If there are more than 1 items in slot then we move the items 1 slot to omit deleted item slot.
	if node.Count > 1 {
		if position < node.Count-1 {
			moveArrayElements(node.Slots,
				position,
				position+1,
				node.Count-position-1)
		}
		// Nullify the last slot.
		node.Count--
		node.Slots[node.Count] = Item[TK, TV]{}
		// We don't fix the children since there are no children at this scenario.
		btree.saveNode(node)
		return nil
	}
	if node.isRootNode() {
		// Delete the single item in root node.
		node.Count = 0
		node.Slots[0] = Item[TK, TV]{}
		btree.setCurrentItemID(sop.NilUUID, 0)
		btree.saveNode(node)
		return nil
	}
	if ok, err := node.unlinkNodeWithNilChild(ctx, btree); ok || err != nil {
		return err
	}
	return node.unlink(ctx, btree)
}

func (node *Node[TK, TV]) isNilChildren() bool {
	for _, id := range node.ChildrenIDs {
		if id != sop.NilUUID {
			return false
		}
	}
	return true
}

// isThereVacantSlotInLeft scans left siblings (staying within the leaf branch when balancing
// is enabled) to find a node with available capacity. It also reports if the branch is unbalanced.
func (node *Node[TK, TV]) isThereVacantSlotInLeft(ctx context.Context, btree *Btree[TK, TV], isUnBalanced *bool) (bool, error) {
	*isUnBalanced = false
	if !btree.StoreInfo.LeafLoadBalancing {
		return false, nil
	}
	// Start from this node.
	temp := node
	for temp != nil {
		if temp.nodeHasNilChild() {
			return true, nil
		}
		if temp.ChildrenIDs != nil {
			*isUnBalanced = true
			return false, nil
		}
		if !temp.isFull() {
			return true, nil
		}
		var err error
		temp, err = temp.getLeftSibling(ctx, btree)
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

// isThereVacantSlotInRight scans right siblings (staying within the leaf branch when balancing
// is enabled) to find a node with available capacity. It also reports if the branch is unbalanced.
func (node *Node[TK, TV]) isThereVacantSlotInRight(ctx context.Context, btree *Btree[TK, TV], isUnBalanced *bool) (bool, error) {
	*isUnBalanced = false
	if !btree.StoreInfo.LeafLoadBalancing {
		return false, nil
	}
	// Start from this node.
	temp := node
	for temp != nil {
		if temp.nodeHasNilChild() {
			return true, nil
		}
		if temp.ChildrenIDs != nil {
			*isUnBalanced = true
			return false, nil
		}
		if !temp.isFull() {
			return true, nil
		}
		var err error
		temp, err = temp.getRightSibling(ctx, btree)
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

// distributeToLeft performs a counter-clockwise rotation when needed to move
// an item into a left sibling with a free slot, updating parent separators.
func (node *Node[TK, TV]) distributeToLeft(ctx context.Context, btree *Btree[TK, TV], item *Item[TK, TV]) error {
	if ok := node.distributeItemOnNodeWithNilChild(btree, item); ok {
		return nil
	}
	if node.isFull() {
		// Counter-clockwise rotation.
		//  ----
		//  |  |
		//  -> |
		// NOTE: we don't check for nil returns as this method is called only when there is a vacancy on the left.
		parent, err := node.getParent(ctx, btree)
		if err != nil {
			return err
		}

		indexOfNode := parent.getIndexOfChild(node)
		if indexOfNode > parent.Count {
			return nil
		}

		// Let the controller make another call to distribute the item to the left.
		btree.distributeAction.sourceNode, err = node.getLeftSibling(ctx, btree)
		if err != nil {
			return err
		}

		btree.distributeAction.item = parent.Slots[indexOfNode-1]
		btree.distributeAction.distributeToLeft = true

		// Update Parent (remove node and add updated one).
		parent.Slots[indexOfNode-1] = node.Slots[0]
		btree.saveNode(parent)
		moveArrayElements(node.Slots, 0, 1, btree.getSlotLength()-1)
	} else {
		node.Count++
	}
	node.Slots[node.Count-1] = *item
	btree.saveNode(node)
	return nil
}

// distributeToRight performs a clockwise rotation when needed to move
// an item into a right sibling with a free slot, updating parent separators.
func (node *Node[TK, TV]) distributeToRight(ctx context.Context, btree *Btree[TK, TV], item *Item[TK, TV]) error {
	if ok := node.distributeItemOnNodeWithNilChild(btree, item); ok {
		return nil
	}
	if node.isFull() {
		// Clockwise rotation.
		//  ----
		//  |  |
		//  | <-
		parent, err := node.getParent(ctx, btree)
		if err != nil {
			return err
		}
		i := parent.getIndexOfChild(node)

		// Let the controller make another call to distribute the item to the right.
		btree.distributeAction.sourceNode, err = node.getRightSibling(ctx, btree)
		if err != nil {
			return err
		}
		btree.distributeAction.item = parent.Slots[i]
		btree.distributeAction.distributeToLeft = false

		parent.Slots[i] = node.Slots[node.Count-1]
		btree.saveNode(parent)
	} else {
		node.Count++
	}
	moveArrayElements(node.Slots, 1, 0, btree.getSlotLength()-1)
	node.Slots[0] = *item
	btree.saveNode(node)
	return nil
}

// promote inserts the separator (btree.tempParent) into this node. If the node
// is full, it splits into left/right siblings and propagates the promotion up
// the tree (splitting ancestors as needed). Root splits increase tree height.
func (node *Node[TK, TV]) promote(ctx context.Context, btree *Btree[TK, TV], indexPosition int) error {
	noOfOccupiedSlots := node.Count
	index := indexPosition
	if noOfOccupiedSlots < btree.getSlotLength() {
		// Node is not yet full; insert the parent.
		shiftSlots(node.Slots, index, noOfOccupiedSlots)
		if index > noOfOccupiedSlots {
			index = noOfOccupiedSlots
		}
		node.Slots[index] = btree.tempParent

		// Insert the left child.
		node.ChildrenIDs[index] = btree.tempParentChildren[0]
		// Insert the right child.
		shiftSlots(node.ChildrenIDs, index+1, noOfOccupiedSlots+1)
		node.Count++
		node.ChildrenIDs[index+1] = btree.tempParentChildren[1]
		btree.saveNode(node)
		return nil
	}

	// Insert into temp slots: node is full, use tempSlots.
	// NOTE: ensure the node and its children being promoted point to the correct
	// new ParentID as recursive node breakups occur...
	copyArrayElements(btree.tempSlots, node.Slots, btree.getSlotLength())
	shiftSlots(btree.tempSlots, index, btree.getSlotLength())
	btree.tempSlots[index] = btree.tempParent
	copyArrayElements(btree.tempChildren, node.ChildrenIDs, btree.getSlotLength()+1)

	// Insert the left child.
	btree.tempChildren[index] = btree.tempParentChildren[0]
	// Insert the right child.
	shiftSlots(btree.tempChildren, index+1, noOfOccupiedSlots+1)
	btree.tempChildren[index+1] = btree.tempParentChildren[1]

	// Try to break up the node into 2 siblings.
	slotsHalf := btree.getSlotLength() >> 1

	if node.isRootNode() {
		// No parent: break up this node into two children and keep node as root.
		leftNode := newNode[TK, TV](btree.getSlotLength())
		leftNode.newID(node.ID)

		rightNode := newNode[TK, TV](btree.getSlotLength())
		rightNode.newID(node.ID)

		// Copy the left half of the slots
		copyArrayElements(leftNode.Slots, btree.tempSlots, slotsHalf)
		leftNode.Count = slotsHalf
		// Copy the right half of the slots
		copyArrayElements(rightNode.Slots, btree.tempSlots[slotsHalf+1:], slotsHalf)
		rightNode.Count = slotsHalf
		leftNode.ChildrenIDs = make([]sop.UUID, btree.getSlotLength()+1)
		rightNode.ChildrenIDs = make([]sop.UUID, btree.getSlotLength()+1)
		// Copy the left half of the children nodes.
		copyArrayElements(leftNode.ChildrenIDs, btree.tempChildren, slotsHalf+1)
		// Copy the right half of the children nodes.
		copyArrayElements(rightNode.ChildrenIDs, btree.tempChildren[slotsHalf+1:], slotsHalf+1)

		// Reset this Node.
		clear(node.Slots)
		clear(node.ChildrenIDs)

		// Make the left sibling parent of its children.
		leftNode.updateChildrenParent(ctx, btree)

		// Make the right sibling parent of its children.
		rightNode.updateChildrenParent(ctx, btree)

		// Copy the middle slot
		node.Slots[0] = btree.tempSlots[slotsHalf]
		node.Count = 1

		// Assign the new children nodes.
		node.ChildrenIDs[0] = leftNode.ID
		node.ChildrenIDs[1] = rightNode.ID
		btree.saveNode(node)
		btree.saveNode(leftNode)
		btree.saveNode(rightNode)
		return nil
	}
	// Prepare this node and the right sibling, then promote the temporary parent node (btree.tempParent).
	// This will be the left sibling.
	rightNode := newNode[TK, TV](btree.getSlotLength())
	rightNode.newID(node.ParentID)
	rightNode.ChildrenIDs = make([]sop.UUID, btree.getSlotLength()+1)

	// Zero out the current slot.
	clear(node.Slots)
	// Zero out this children node pointers.
	clear(node.ChildrenIDs)

	// Copy the left half of the slots to left sibling(this)
	copyArrayElements(node.Slots, btree.tempSlots, slotsHalf)
	node.Count = slotsHalf

	// Copy the right half of the slots to right sibling
	copyArrayElements(rightNode.Slots, btree.tempSlots[slotsHalf+1:], slotsHalf)
	rightNode.Count = slotsHalf
	// Copy the left half of the children nodes.
	copyArrayElements(node.ChildrenIDs, btree.tempChildren, slotsHalf+1)

	// Copy the right half of the children nodes.
	copyArrayElements(rightNode.ChildrenIDs, btree.tempChildren[slotsHalf+1:], slotsHalf+1)

	// Make the right sibling parent of its children.
	if err := rightNode.updateChildrenParent(ctx, btree); err != nil {
		return err
	}
	btree.saveNode(rightNode)
	// Make "all" of the left sibling parent of its children.
	if err := node.updateChildrenParent(ctx, btree); err != nil {
		return err
	}
	btree.saveNode(node)

	// Copy the middle slot.
	btree.tempParent = btree.tempSlots[slotsHalf]
	// Assign the new children nodes.
	btree.tempParentChildren[0] = node.ID
	btree.tempParentChildren[1] = rightNode.ID

	// Trigger another promotion.
	var err error
	btree.promoteAction.targetNode, err = node.getParent(ctx, btree)
	if err != nil {
		return err
	}
	btree.promoteAction.slotIndex = btree.promoteAction.targetNode.getIndexOfChild(node)
	return nil
}

func (node *Node[TK, TV]) newID(parentID sop.UUID) {
	// Set the physical IDs; transaction commit should resolve physical vs logical IDs.
	node.ID = sop.NewUUID()
	node.ParentID = parentID
}

func (node *Node[TK, TV]) getChildID(index int) sop.UUID {
	if len(node.ChildrenIDs) == 0 {
		return sop.NilUUID
	}
	return node.ChildrenIDs[index]
}

// updateChildrenParent fixes ParentID pointers of all existing children to
// reference the provided node after structural changes (split/rotation/promote).
func (node *Node[TK, TV]) updateChildrenParent(ctx context.Context, btree *Btree[TK, TV]) error {
	if !node.hasChildren() {
		return nil
	}
	children, err := node.getChildren(ctx, btree)
	if err != nil {
		return err
	}
	// Make node parent of its children.
	for index := range children {
		if children[index] != nil {
			children[index].ParentID = node.ID
			btree.saveNode(children[index])
		}
	}
	return nil
}

// unlink removes this node from its parent when it becomes empty, pruning
// nil child pointers and deleting the node from the repository.
func (node *Node[TK, TV]) unlink(ctx context.Context, btree *Btree[TK, TV]) error {
	p, err := node.getParent(ctx, btree)
	if err != nil {
		return err
	}
	if p == nil || !p.hasChildren() {
		return nil
	}
	// Prune empty children.
	i := p.getIndexOfChild(node)
	p.ChildrenIDs[i] = sop.NilUUID
	if p.isNilChildren() {
		p.ChildrenIDs = nil
	}
	btree.saveNode(p)
	btree.removeNode(node)
	return nil
}

// copyArrayElements is a helper function for internal use only.
func copyArrayElements[T any](destination, source []T, count int) {
	if source == nil || destination == nil || count <= 0 {
		return
	}
	copy(destination[:count], source[:count])
}

func shiftSlots[T any](array []T, position int, noOfOccupiedSlots int) {
	if position < noOfOccupiedSlots {
		// Create a vacant slot by shifting node contents one slot.
		moveArrayElements(array, position+1, position, noOfOccupiedSlots-position)
	}
}

// moveArrayElements is a helper function for internal use only.
func moveArrayElements[T any](array []T, destStartIndex, srcStartIndex, count int) {
	if array == nil || count <= 0 || destStartIndex >= len(array) || srcStartIndex >= len(array) || destStartIndex < 0 || srcStartIndex < 0 {
		return
	}
	endSrc := srcStartIndex + count
	if endSrc > len(array) {
		endSrc = len(array)
	}
	copy(array[destStartIndex:], array[srcStartIndex:endSrc])
}

func (node *Node[TK, TV]) isFull() bool {
	return node.Count >= len(node.Slots)
}

func (node *Node[TK, TV]) insertSlotItem(item *Item[TK, TV], position int) {
	copy(node.Slots[position+1:], node.Slots[position:])
	node.Slots[position] = *item
	node.Count++
}

func (node *Node[TK, TV]) getIndexToInsertTo(btree *Btree[TK, TV], item *Item[TK, TV]) (int, bool) {
	if node.Count == 0 {
		// Empty node.
		return 0, false
	}
	index := sort.Search(node.Count, func(index int) bool {
		return btree.compare(node.Slots[index].Key, item.Key) >= 0
	})
	if btree.isUnique() {
		i := index
		if i >= node.Count {
			i--
		}
		// Returns index in slot that is available for insert.
		// Also returns true if an existing item with such key is found.
		return index, btree.compare(node.Slots[i].Key, item.Key) == 0
	}
	// Returns index in slot that is available for insert.
	return index, false
}

// Transaction resolves fetching nodes via logical ID vs physical ID. In a transaction,
// newly created nodes use sop.UUIDs that later become logical IDs at commit time.
// When working with child logical IDs (saved in the backend), convert logical to physical IDs.
func (node *Node[TK, TV]) getChild(ctx context.Context, btree *Btree[TK, TV], childSlotIndex int) (*Node[TK, TV], error) {
	id := node.getChildID(childSlotIndex)
	if id == sop.NilUUID {
		return nil, nil
	}
	return btree.getNode(ctx, id)
}

func (node *Node[TK, TV]) getChildren(ctx context.Context, btree *Btree[TK, TV]) ([]*Node[TK, TV], error) {
	children := make([]*Node[TK, TV], len(node.ChildrenIDs))
	var err error
	for i, id := range node.ChildrenIDs {
		if id == sop.NilUUID {
			continue
		}
		children[i], err = btree.getNode(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	return children, nil
}

// hasChildren returns true if the node has children.
func (node *Node[TK, TV]) hasChildren() bool {
	return len(node.ChildrenIDs) > 0
}

// isRootNode returns true if the node has no parent.
func (node *Node[TK, TV]) isRootNode() bool {
	return node.ParentID == sop.NilUUID
}

func (node *Node[TK, TV]) getParent(ctx context.Context, btree *Btree[TK, TV]) (*Node[TK, TV], error) {
	if node.ParentID.IsNil() {
		return nil, nil
	}
	return btree.getNode(ctx, node.ParentID)
}

// getLeftSibling returns the left sibling, or nil if the leftmost sibling has been reached.
func (node *Node[TK, TV]) getLeftSibling(ctx context.Context, btree *Btree[TK, TV]) (*Node[TK, TV], error) {
	index, err := node.getIndexOfNode(ctx, btree)
	if err != nil {
		return nil, err
	}
	p, err := node.getParent(ctx, btree)
	if err != nil {
		return nil, err
	}
	if p != nil {
		// If we are not at the leftmost sibling yet..
		if index > 0 && index <= p.Count {
			return p.getChild(ctx, btree, index-1)
		}
	}
	// Leftmost already reached.
	return nil, nil
}

// getRightSibling returns the right sibling, or nil if the rightmost sibling has been reached.
func (node *Node[TK, TV]) getRightSibling(ctx context.Context, btree *Btree[TK, TV]) (*Node[TK, TV], error) {
	index, err := node.getIndexOfNode(ctx, btree)
	if err != nil {
		return nil, err
	}
	p, err := node.getParent(ctx, btree)
	if err != nil {
		return nil, err
	}
	if p != nil && index >= 0 {
		// If we are not at the rightmost sibling yet..
		if index < p.Count {
			return p.getChild(ctx, btree, index+1)
		}
	}
	// Rightmost already reached.
	return nil, nil
}

// getIndexOfNode returns the index of this node relative to its parent.
func (node *Node[TK, TV]) getIndexOfNode(ctx context.Context, btree *Btree[TK, TV]) (int, error) {
	parent, err := node.getParent(ctx, btree)
	if err != nil {
		return -1, err
	}
	if parent != nil {
		return parent.getIndexOfChild(node), nil
	}
	// Return 0 if called on the root node; callers should normally avoid calling this for the root.
	return 0, nil
}

// getIndexOfChild returns the index of the given child within this node's ChildrenIDs.
func (node *Node[TK, TV]) getIndexOfChild(child *Node[TK, TV]) int {
	parent := node
	// Ensure we don't access an invalid child index; recompute when unknown or mismatched.
	if parent.ChildrenIDs != nil && (child.indexOfNode == -1 || child.ID != parent.ChildrenIDs[child.indexOfNode]) {
		for child.indexOfNode = 0; child.indexOfNode <= len(parent.Slots); child.indexOfNode++ {
			if parent.ChildrenIDs[child.indexOfNode].IsNil() {
				continue
			}
			if parent.ChildrenIDs[child.indexOfNode] == child.ID {
				break
			}
		}
	}
	return child.indexOfNode
}
