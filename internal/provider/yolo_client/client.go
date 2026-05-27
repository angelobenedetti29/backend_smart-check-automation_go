package yolo_client

import (
	"log"
	"math/rand"
	"time"
)

// YOLOClient simulates a client interface for an external YOLO deep learning model.
// In a full implementation, this sends image binaries or paths to a python-based YOLO service.
type YOLOClient struct {
	endpoint string
}

// DetectionResult holds simulated details of detected elements.
type DetectionResult struct {
	PieceDetected bool    `json:"piece_detected"`
	DefectFound   bool    `json:"defect_found"`
	Confidence    float64 `json:"confidence"`
	ProcessingMs  int64   `json:"processing_ms"`
}

// NewYOLOClient initializes a new YOLOClient.
func NewYOLOClient(endpoint string) *YOLOClient {
	return &YOLOClient{
		endpoint: endpoint,
	}
}

// InspectConveyorLine triggers a simulated YOLO analysis on the kiln's feed/conveyor line.
func (c *YOLOClient) InspectConveyorLine() DetectionResult {
	log.Printf("[YOLO Adapter] Requesting real-time vision model evaluation at %s", c.endpoint)
	
	// Simulate minor latency of object-detection models (25-50ms)
	time.Sleep(30 * time.Millisecond)

	// Simple pseudo-randomized defect detector for verification purposes
	source := rand.NewSource(time.Now().UnixNano())
	r := rand.New(source)

	defect := r.Float64() > 0.85 // 15% probability of visual defect detection
	confidence := 0.82 + (r.Float64() * 0.17) // 82% to 99% confidence

	return DetectionResult{
		PieceDetected: true,
		DefectFound:   defect,
		Confidence:    confidence,
		ProcessingMs:  time.Now().UnixNano() % 45,
	}
}
