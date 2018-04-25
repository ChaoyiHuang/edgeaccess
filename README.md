# edgeaccess
Setup sync web-socket connection between edged and edgeaccess.

Fundamental flow:
1. Start two edgeaccess in two terminals
   ./edgeaccess -f ea1.conf
   
   ./edgeaccess -f ea2.conf

2. Start placement in another terminal, placement will do health check for edgeaccesses
   ./placement -f p.conf

3. Start edged in another two terminals. Edged will ask for placement which edgeaccess to connect. Edged will establish web-socket connection (sync connection for downlink, uplink respectively) to edgeaccess via placement's decision.

   ./edged -f ed1.conf
   
   ./edged -f ed2.conf

4. you can ping edged via edgeaccess through downLink sync connection
   curl "http://127.0.0.1:8899/v1.0/ping2edged?edgenode_id=22&msg=hello"
   curl "http://127.0.0.1:8898/v1.0/ping2edged?edgenode_id=333&msg=hello"

5. edged will periodicly generate the statistic of local CPU utilization, and send the result to edgeaccess via upLink sync connection.

   2018/04/25 15:29:20 sendReq2EdgeAccess, req: {"id":41,"timestamp":1524641360,"body":"8.50","reply":""}
   2018/04/25 15:29:20 sendReq2EdgeAccess, replied {41 1524641360 8.50 touched by EdgeAccess at2018-04-25 15:29:20}
   
6. stop one edgeaccess, all edged connection will be shifted to another edgeaccess.

7. there is some issue in ping to edged after edgeaccess is restarted, this issue is left to someone eager to dig.
