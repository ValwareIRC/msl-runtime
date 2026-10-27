; A tour of script-created UI in mslrt: a dialog, a listbox window and a
; picture window. Run with:  mslrt examples/gui-demo.mrc

on *:START:{
  dialog -m demo demo
  window -l @people 30 420 260 200
  aline @people Alice
  aline @people Bob
  aline @people Carol
  window -p @canvas 320 420 300 200
  drawrect -f @canvas 12 1 10 10 120 70
  drawrect -ef @canvas 4 1 150 20 120 80
  drawline @canvas 1 3 10 150 290 150
  drawtext @canvas 1 Arial 14 20 160 Drawn by a script
}

dialog demo {
  title "mslrt dialog demo"
  size -1 -1 160 120
  option dbu
  text "Your name:", 1, 5 8 40 10
  edit "Guest", 2, 48 6 105 12
  check "Send greetings", 3, 5 24 80 10
  radio "Morning", 4, 5 38 45 10, group
  radio "Evening", 5, 55 38 45 10
  list 6, 5 52 148 40, sort
  button "OK", 7, 60 100 45 14, ok
  button "Cancel", 8, 110 100 45 14, cancel
  menu "&File", 20
  item "&About", 21
}

on *:DIALOG:demo:init:0:{
  didtok demo 6 44 Zeta,Alpha,Mu
  did -c demo 3
  did -c demo 4
}
on *:DIALOG:demo:sclick:7:{ echo -s Hello $did(demo,2) $+ ! }
on *:DIALOG:demo:menu:21:{ echo -s mslrt $version }

menu @canvas {
  sclick:drawdot @canvas 9 6 $mouse.x $mouse.y
  Clear:clear @canvas
}
