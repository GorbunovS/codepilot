<template>
  <div class="discount_promo">
    <h3>DiscountPromo</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// DiscountPromoPanel — панель «скидка»: применяет промокод к заказу.
export default {
  name: "DiscountPromoPanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (скидка).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.discount_promo { padding: 0.5rem; }
</style>
