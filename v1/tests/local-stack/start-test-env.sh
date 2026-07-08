#!/bin/bash

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}🚀 Starting HOG Local Development Stack${NC}"
echo ""

# Change to the local-stack directory
cd "$(dirname "$0")"

# Start services
echo -e "${YELLOW}Starting Podman services...${NC}"
podman-compose up -d --build

# Wait for services to be ready
echo ""
echo -e "${YELLOW}Waiting for services to be ready...${NC}"
sleep 10

# Check if services are running
if podman-compose ps | grep -q "Up"; then
    echo ""
    echo -e "${GREEN}✅ Services started successfully!${NC}"
    echo ""
    echo -e "${GREEN}📋 Service Status:${NC}"
    podman-compose ps
    echo ""

    # Start log collector in background
    echo -e "${YELLOW}Starting log collector for Grafana...${NC}"
    nohup ./collect-logs.sh > /tmp/hog-log-collector.log 2>&1 &
    LOG_COLLECTOR_PID=$!
    echo -e "${GREEN}   Log collector started (PID: $LOG_COLLECTOR_PID)${NC}"
    echo ""

    echo -e "${GREEN}🌐 Access Points:${NC}"
    echo -e "   ${BLUE}http://localhost:3000${NC}  - Test UI"
    echo -e "   ${BLUE}http://localhost:3001${NC}  - Grafana (admin/admin)"
    echo -e "   ${BLUE}http://localhost:9090${NC}  - Prometheus"
    echo -e "   ${BLUE}http://localhost:12345${NC} - Alloy UI"
    echo ""
    echo -e "${GREEN}📊 View logs:${NC}"
    echo -e "   ${BLUE}podman-compose logs -f${NC}"
    echo ""
    echo -e "${GREEN}🛑 Stop services:${NC}"
    echo -e "   ${BLUE}podman-compose down${NC}"
    echo ""

    # Offer to open browser
    read -p "Open browser now? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        if command -v open &> /dev/null; then
            open http://localhost:3000
            open http://localhost:3001
        elif command -v xdg-open &> /dev/null; then
            xdg-open http://localhost:3000
            xdg-open http://localhost:3001
        else
            echo "Please open http://localhost:3000 and http://localhost:3001 in your browser"
        fi
    fi
else
    echo -e "${YELLOW}⚠️  Some services may not have started correctly.${NC}"
    echo "Check logs with: podman-compose logs"
fi
