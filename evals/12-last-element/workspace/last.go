package last

// Last returns the last element of nums.
func Last(nums []int) (int, bool) {
	if len(nums) == 0 {
		return 0, false
	}
	return nums[len(nums)], false
}
