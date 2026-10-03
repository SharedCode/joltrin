# VHS tape for docs/assets/ltl-barrier.gif
# Records a real run of examples/verify_barrier, not a mockup: every line of
# output in the resulting GIF is the actual stdout of verify blocking (and
# then allowing) drop_prod_db in real time. Regenerate with:
#   vhs docs/assets/ltl-barrier.tape
# from the repo root.

Output docs/assets/ltl-barrier.gif

Set Shell "bash"
Set FontSize 16
Set Width 900
Set Height 560
Set Theme "Dracula"
Set Padding 20

Type "go run ./examples/verify_barrier"
Enter
Sleep 6s
