<?php
// construct: enums (pure + backed)
// minphp: 8.1
// maxphp: 8.4
enum Suit: string {
    case Hearts = 'H';
    case Spades = 'S';
    public function color(): string {
        return match($this) {
            Suit::Hearts => 'red',
            Suit::Spades => 'black',
        };
    }
}
enum Status {
    case Active;
    case Closed;
    public function label(): string {
        return match($this) {
            Status::Active => 'on',
            Status::Closed => 'off',
        };
    }
}
function probe() {
    $s = Suit::Hearts;
    $t = Suit::from('S');
    $st = Status::Active;
    return $s->value . ',' . $s->color() . '|' . $t->name . ',' . $t->color()
        . '|' . $st->label() . '|' . count(Suit::cases());
}
echo probe();
