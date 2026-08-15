package market

import "testing"

func TestWriteOrderValidateRequiresOneBasedComponents(t *testing.T) {
	tests := []struct {
		name    string
		order   WriteOrder
		wantErr bool
	}{
		{name: "first valid order", order: WriteOrder{SwitchVersion: 1, WriteSequence: 1}},
		{name: "later valid order", order: WriteOrder{SwitchVersion: 2, WriteSequence: 4}},
		{name: "zero switch version", order: WriteOrder{WriteSequence: 1}, wantErr: true},
		{name: "negative switch version", order: WriteOrder{SwitchVersion: -1, WriteSequence: 1}, wantErr: true},
		{name: "zero write sequence", order: WriteOrder{SwitchVersion: 1}, wantErr: true},
		{name: "negative write sequence", order: WriteOrder{SwitchVersion: 1, WriteSequence: -1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.order.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWriteOrderCompareUsesLexicographicOrder(t *testing.T) {
	tests := []struct {
		name  string
		left  WriteOrder
		right WriteOrder
		want  int
	}{
		{
			name:  "equal",
			left:  WriteOrder{SwitchVersion: 2, WriteSequence: 4},
			right: WriteOrder{SwitchVersion: 2, WriteSequence: 4},
		},
		{
			name:  "lower switch wins over later write",
			left:  WriteOrder{SwitchVersion: 1, WriteSequence: 99},
			right: WriteOrder{SwitchVersion: 2, WriteSequence: 1},
			want:  -1,
		},
		{
			name:  "higher switch wins over earlier write",
			left:  WriteOrder{SwitchVersion: 2, WriteSequence: 1},
			right: WriteOrder{SwitchVersion: 1, WriteSequence: 99},
			want:  1,
		},
		{
			name:  "lower write",
			left:  WriteOrder{SwitchVersion: 2, WriteSequence: 3},
			right: WriteOrder{SwitchVersion: 2, WriteSequence: 4},
			want:  -1,
		},
		{
			name:  "higher write",
			left:  WriteOrder{SwitchVersion: 2, WriteSequence: 4},
			right: WriteOrder{SwitchVersion: 2, WriteSequence: 3},
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.left.Validate(); err != nil {
				t.Fatalf("left Validate() error = %v", err)
			}
			if err := tt.right.Validate(); err != nil {
				t.Fatalf("right Validate() error = %v", err)
			}
			if got := tt.left.Compare(tt.right); got != tt.want {
				t.Fatalf("Compare() = %d, want %d", got, tt.want)
			}
		})
	}
}
