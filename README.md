# kunkka

Quick and dirty implementation of the BitTorrent protocol.

I was able to download Debian ISO with this program, but I think that there are still some concurrency
bugs that make the program hang forever once it finishes download. Bencode decoder could have an
interface to `encoding/json`, but I was too lazy to implement the unmarshalling logic. Endgame mode is
not implemented, and the program also assumes that at least 1 peer will have the wanted piece.

It's named after a [Dota 2 hero](https://dota2.fandom.com/wiki/Kunkka).

## BEPs

Implemented:
- [x] 03 - The BitTorrent Protocol Specification
- [x] 23 - Tracker Returns Compact Peer Lists

TODO:
- [ ] 05 - DHT Protocol
- [ ] 10 - Extension Protocol
- [ ] 09 - Extension for Peers to Send Metadata Files
- [ ] 15 - UDP Tracker Protocol
